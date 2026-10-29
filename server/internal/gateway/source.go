package gateway

import (
	"context"
	"time"

	lensv1 "github.com/portlink/lens/server/internal/gen/lens/v1"
	"github.com/portlink/lens/server/internal/model"
)

// View is everything a source needs to know about what the client can see.
type View struct {
	BBox     model.BBox
	Zoom     float64
	All      bool  // Phase 3 (firehose): ignore the box
	CursorMs int64 // history only
}

// Source returns the vessels the client should currently see, the tier they came from,
// and the data time they represent.
type Source interface {
	Snapshot(ctx context.Context, v View, dst []model.VesselState) ([]model.VesselState, lensv1.Tier, int64, error)
}

// liveSource reads the in-memory world. Shared by all clients.
type liveSource struct{ world *World }

func (l *liveSource) Snapshot(_ context.Context, v View, dst []model.VesselState) ([]model.VesselState, lensv1.Tier, int64, error) {
	return l.world.Query(v.BBox, v.All, dst), lensv1.Tier_TIER_LIVE, time.Now().UnixMilli(), nil
}

// historySource is per client. It caches the last query so a paused cursor, or a cursor that is
// still inside the same bucket, costs zero database work.
type historySource struct {
	st     historyStore
	key    histKey
	cached []model.VesselState
}

type histKey struct {
	view string
	tqMs int64 // cursor floored to the tier's bucket width
	bbox model.BBox
}

func (h *historySource) Snapshot(ctx context.Context, v View, dst []model.VesselState) ([]model.VesselState, lensv1.Tier, int64, error) {
	spec := tierForZoom(v.Zoom)
	w := spec.width.Milliseconds()
	tq := v.CursorMs - v.CursorMs%w
	k := histKey{spec.view, tq, v.BBox}
	if k != h.key {
		qctx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		to := time.UnixMilli(tq)
		rows, err := h.st.AsOf(qctx, spec.view, to.Add(-spec.lookback), to, v.BBox)
		if err != nil {
			return dst, spec.tier, v.CursorMs, err
		}
		h.cached, h.key = rows, k
	}
	// ts is the exact cursor (not tq) so the client's scrubber moves smoothly during playback.
	return append(dst, h.cached...), spec.tier, v.CursorMs, nil
}
