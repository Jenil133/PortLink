package feed

import (
	"time"

	lensv1 "github.com/portlink/lens/server/internal/gen/lens/v1"
)

// Source produces the sample of vessel i at time t. It must be a pure function of (i, t)
// so that live mode and backfill mode agree and the feeder holds no state across restarts.
// ok=false means the vessel has no position at that time.
type Source interface {
	Len() int
	SampleAt(i int, t time.Time) (s *lensv1.VesselSample, ok bool)
}
