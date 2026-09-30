package store

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/portlink/lens/server/internal/model"
)

// Only these view names may ever be interpolated into SQL.
var allowedViews = map[string]bool{"samples_1s": true, "samples_1m": true, "samples_10m": true}

// AsOf returns the state of every vessel inside the box at time `to`, using buckets in [from, to).
// The inner query picks each vessel's newest bucket first and the outer query applies the box,
// so a vessel that has already left the box is not shown at its last in-box position.
func (s *Store) AsOf(ctx context.Context, view string, from, to time.Time, b model.BBox) ([]model.VesselState, error) {
	if !allowedViews[view] {
		return nil, fmt.Errorf("unknown view %q", view)
	}
	q := fmt.Sprintf(`
		SELECT a.mmsi, a.bucket, a.lat, a.lon, a.sog, a.cog, a.heading,
		       a.engine_rpm, a.fuel_rate_lph, a.engine_temp_c, a.fuel_level_pct,
		       COALESCE(v.name, ''), COALESCE(v.vessel_type, 0)
		FROM (
			SELECT DISTINCT ON (mmsi) *
			FROM %s
			WHERE bucket >= $1 AND bucket < $2
			ORDER BY mmsi, bucket DESC
		) a
		LEFT JOIN vessels v ON v.mmsi = a.mmsi
		WHERE a.lat BETWEEN $3 AND $4 AND a.lon BETWEEN $5 AND $6`, view)
	rows, err := s.Pool.Query(ctx, q, from, to, b.MinLat, b.MaxLat, b.MinLon, b.MaxLon)
	if err != nil {
		return nil, err
	}
	return scanStates(rows)
}

type SeriesRow struct {
	T                           time.Time
	Lat, Lon                    float64
	Sog, RPM, Fuel, Temp, Level float32
}

// SeriesRows returns one vessel's buckets in [from, to), oldest first.
func (s *Store) SeriesRows(ctx context.Context, view string, mmsi int64, from, to time.Time) ([]SeriesRow, error) {
	if !allowedViews[view] {
		return nil, fmt.Errorf("unknown view %q", view)
	}
	q := fmt.Sprintf(`
		SELECT bucket, lat, lon, sog, engine_rpm, fuel_rate_lph, engine_temp_c, fuel_level_pct
		FROM %s WHERE mmsi = $1 AND bucket >= $2 AND bucket < $3 ORDER BY bucket`, view)
	rows, err := s.Pool.Query(ctx, q, mmsi, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SeriesRow
	for rows.Next() {
		var r SeriesRow
		if err := rows.Scan(&r.T, &r.Lat, &r.Lon, &r.Sog, &r.RPM, &r.Fuel, &r.Temp, &r.Level); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// HistoryStart is the oldest timestamp the scrubber may reach: the oldest raw row, but not older than `hours`.
func (s *Store) HistoryStart(ctx context.Context, hours int) (time.Time, error) {
	var t time.Time
	err := s.Pool.QueryRow(ctx, `
		SELECT COALESCE(min(ts), now()) FROM vessel_samples
		WHERE ts > now() - make_interval(hours => $1)`, hours).Scan(&t)
	return t, err
}

// LatestStates loads the newest sample per vessel from the last 2 minutes.
// The gateway calls it once at start so the map is not empty until the first samples arrive.
func (s *Store) LatestStates(ctx context.Context) ([]model.VesselState, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT DISTINCT ON (a.mmsi) a.mmsi, a.ts, a.lat, a.lon, a.sog, a.cog, a.heading,
		       a.engine_rpm, a.fuel_rate_lph, a.engine_temp_c, a.fuel_level_pct,
		       COALESCE(v.name, ''), COALESCE(v.vessel_type, 0)
		FROM vessel_samples a LEFT JOIN vessels v ON v.mmsi = a.mmsi
		WHERE a.ts > now() - INTERVAL '2 minutes'
		ORDER BY a.mmsi, a.ts DESC`)
	if err != nil {
		return nil, err
	}
	return scanStates(rows)
}

// scanStates reads rows shaped like the AsOf and LatestStates select lists and closes them.
func scanStates(rows pgx.Rows) ([]model.VesselState, error) {
	defer rows.Close()
	var out []model.VesselState
	for rows.Next() {
		var st model.VesselState
		var mmsi int64
		var at time.Time
		var vtype int32
		if err := rows.Scan(&mmsi, &at, &st.Lat, &st.Lon, &st.Sog, &st.Cog, &st.Heading,
			&st.EngineRPM, &st.FuelRateLph, &st.EngineTempC, &st.FuelLevelPct, &st.Name, &vtype); err != nil {
			return nil, err
		}
		st.MMSI, st.TsMs, st.VesselType = uint32(mmsi), at.UnixMilli(), uint32(vtype)
		out = append(out, st)
	}
	return out, rows.Err()
}
