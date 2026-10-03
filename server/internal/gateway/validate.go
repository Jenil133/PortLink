package gateway

import (
	"math"

	lensv1 "github.com/portlink/lens/server/internal/gen/lens/v1"
)

// normalize validates a Subscribe and fills defaults in place. A non-empty return value is an
// error message for ServerError code 2, and the previous subscription stays active.
func normalize(sub *lensv1.Subscribe, historyHours int) string {
	if !validBBox(sub.Bbox) {
		return "invalid bbox"
	}
	if math.IsNaN(float64(sub.Zoom)) {
		return "invalid zoom"
	}
	sub.Zoom = float32(math.Min(22, math.Max(0, float64(sub.Zoom))))
	switch sub.Mode {
	case lensv1.Mode_MODE_UNSPECIFIED:
		sub.Mode = lensv1.Mode_MODE_LIVE
	case lensv1.Mode_MODE_LIVE:
	case lensv1.Mode_MODE_HISTORY:
		if sub.CursorMs <= 0 {
			return "history mode needs cursor_ms"
		}
	default:
		return "unknown mode"
	}
	if math.IsNaN(float64(sub.PlaybackRate)) || sub.PlaybackRate < 0 {
		sub.PlaybackRate = 0
	}
	if sub.PlaybackRate > 600 {
		sub.PlaybackRate = 600
	}
	maxWindow := int64(historyHours) * 3600 * 1000
	if sub.SeriesWindowMs < 0 {
		sub.SeriesWindowMs = 0
	}
	if sub.SeriesWindowMs > maxWindow {
		sub.SeriesWindowMs = maxWindow
	}
	if sub.SeriesMaxPoints == 0 {
		sub.SeriesMaxPoints = 600
	}
	sub.SeriesMaxPoints = min(2000, max(50, sub.SeriesMaxPoints))
	sub.Firehose = false // Phase 3 implements firehose, ignore the flag until then
	return ""
}

func validBBox(b *lensv1.BBox) bool {
	if b == nil {
		return false
	}
	for _, v := range []float64{b.MinLon, b.MinLat, b.MaxLon, b.MaxLat} {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return false
		}
	}
	return b.MinLon < b.MaxLon && b.MinLat < b.MaxLat &&
		b.MinLat >= -90 && b.MaxLat <= 90 && b.MinLon >= -360 && b.MaxLon <= 360
}
