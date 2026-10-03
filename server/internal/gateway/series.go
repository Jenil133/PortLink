package gateway

import (
	"context"
	"math"
	"sort"
	"time"

	lensv1 "github.com/portlink/lens/server/internal/gen/lens/v1"
	"github.com/portlink/lens/server/internal/lttb"
)

type seriesKey struct {
	mmsi      uint32
	windowMs  int64
	maxPoints uint32
}

const seriesRefreshMs = 10_000

// maybeBuildSeries returns a Series when a vessel is selected and either the request changed
// or the last one is older than 10 s. The window always ends at "now": the chart shows the whole
// timeline and the client draws its own cursor marker on top.
func (c *client) maybeBuildSeries(ctx context.Context, sub *lensv1.Subscribe, nowMs int64) *lensv1.Series {
	if sub.SelectedMmsi == 0 || sub.SeriesWindowMs == 0 {
		c.seriesKey = seriesKey{}
		return nil
	}
	k := seriesKey{sub.SelectedMmsi, sub.SeriesWindowMs, sub.SeriesMaxPoints}
	if k == c.seriesKey && nowMs-c.seriesAtMs < seriesRefreshMs {
		return nil
	}
	c.seriesKey, c.seriesAtMs = k, nowMs // set before the query so a failing DB is retried every 10 s, not every tick

	window := time.Duration(sub.SeriesWindowMs) * time.Millisecond
	spec := tierForWindow(window)
	end := time.UnixMilli(nowMs)
	start := end.Add(-window)
	qctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	rows, err := c.srv.st.SeriesRows(qctx, spec.view, int64(sub.SelectedMmsi), start, end)
	if err != nil {
		return nil
	}

	n := len(rows)
	xs := make([]float64, n) // seconds since start
	lat, lon := make([]float64, n), make([]float64, n)
	cols := map[lensv1.Metric][]float64{
		lensv1.Metric_METRIC_SOG: make([]float64, n), lensv1.Metric_METRIC_ENGINE_RPM: make([]float64, n),
		lensv1.Metric_METRIC_FUEL_RATE: make([]float64, n), lensv1.Metric_METRIC_ENGINE_TEMP: make([]float64, n),
		lensv1.Metric_METRIC_FUEL_LEVEL: make([]float64, n),
	}
	for i, r := range rows {
		xs[i] = r.T.Sub(start).Seconds()
		lat[i], lon[i] = r.Lat, r.Lon
		cols[lensv1.Metric_METRIC_SOG][i] = float64(r.Sog)
		cols[lensv1.Metric_METRIC_ENGINE_RPM][i] = float64(r.RPM)
		cols[lensv1.Metric_METRIC_FUEL_RATE][i] = float64(r.Fuel)
		cols[lensv1.Metric_METRIC_ENGINE_TEMP][i] = float64(r.Temp)
		cols[lensv1.Metric_METRIC_FUEL_LEVEL][i] = float64(r.Level)
	}

	out := &lensv1.Series{
		ViewId: sub.ViewId, Mmsi: sub.SelectedMmsi, StartMs: start.UnixMilli(), EndMs: nowMs,
		Tier: spec.tier, RawPoints: uint32(n),
	}
	maxPts := int(sub.SeriesMaxPoints)
	// Fixed metric order so the client can rely on it.
	for _, m := range []lensv1.Metric{
		lensv1.Metric_METRIC_SOG, lensv1.Metric_METRIC_ENGINE_RPM, lensv1.Metric_METRIC_FUEL_RATE,
		lensv1.Metric_METRIC_ENGINE_TEMP, lensv1.Metric_METRIC_FUEL_LEVEL,
	} {
		ys := cols[m]
		ms := &lensv1.MetricSeries{Metric: m}
		for _, i := range lttb.Indices(xs, ys, maxPts) {
			ms.TOffsetS = append(ms.TOffsetS, uint32(xs[i]))
			ms.Values = append(ms.Values, float32(ys[i]))
		}
		out.Metrics = append(out.Metrics, ms)
	}
	// Track: union of the LTTB picks for latitude and longitude, each with half the budget.
	keep := map[int]struct{}{}
	for _, i := range lttb.Indices(xs, lat, maxPts/2) {
		keep[i] = struct{}{}
	}
	for _, i := range lttb.Indices(xs, lon, maxPts/2) {
		keep[i] = struct{}{}
	}
	idx := make([]int, 0, len(keep))
	for i := range keep {
		idx = append(idx, i)
	}
	sort.Ints(idx)
	for _, i := range idx {
		out.Track = append(out.Track, &lensv1.TrackPoint{
			TOffsetS: uint32(xs[i]), LatE6: int32(math.Round(lat[i] * 1e6)), LonE6: int32(math.Round(lon[i] * 1e6)),
		})
	}
	return out
}
