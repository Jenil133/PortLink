package gateway

import (
	"time"

	lensv1 "github.com/portlink/lens/server/internal/gen/lens/v1"
)

type tierSpec struct {
	tier     lensv1.Tier
	view     string
	width    time.Duration
	lookback time.Duration
}

var (
	tier1s  = tierSpec{lensv1.Tier_TIER_1S, "samples_1s", time.Second, 30 * time.Second}
	tier1m  = tierSpec{lensv1.Tier_TIER_1M, "samples_1m", time.Minute, 3 * time.Minute}
	tier10m = tierSpec{lensv1.Tier_TIER_10M, "samples_10m", 10 * time.Minute, 20 * time.Minute}
)

// tierForZoom: the coarsest tier whose bucket is still roughly sub-pixel motion at this zoom.
func tierForZoom(zoom float64) tierSpec {
	switch {
	case zoom >= 11:
		return tier1s
	case zoom >= 7:
		return tier1m
	default:
		return tier10m
	}
}

// tierForWindow: the finest tier that returns at most 25,000 rows for the window.
// LTTB then reduces that to the requested point count.
func tierForWindow(window time.Duration) tierSpec {
	for _, t := range []tierSpec{tier1s, tier1m} {
		if window/t.width <= 25000 {
			return t
		}
	}
	return tier10m
}
