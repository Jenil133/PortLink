package feed

import (
	"math"

	lensv1 "github.com/portlink/lens/server/internal/gen/lens/v1"
)

// splitmix64: cheap, well-mixed integer hash. Used so per-vessel constants and noise
// are deterministic without storing any state.
func hash64(x uint64) uint64 {
	x += 0x9e3779b97f4a7c15
	x = (x ^ (x >> 30)) * 0xbf58476d1ce4e5b9
	x = (x ^ (x >> 27)) * 0x94d049bb133111eb
	return x ^ (x >> 31)
}

func unit(h uint64) float64 { return float64(h%100000) / 100000 } // [0,1)

// FillTelemetry sets the four telemetry fields from physics-flavoured formulas.
func FillTelemetry(s *lensv1.VesselSample) {
	h := hash64(uint64(s.Mmsi))
	rpmPerKn := 4 + 2*unit(h)        // 4 to 6 rpm per knot
	fuelK := 0.06 + 0.06*unit(h>>16) // L/h per knot cubed (drag grows with the cube of speed)
	phase := unit(h >> 32)
	tSec := s.TsMs / 1000
	noise := 2*unit(hash64(uint64(s.Mmsi)<<32|uint64(tSec))) - 1 // [-1,1), changes every second
	sog := float64(s.SogKn)

	s.EngineRpm = float32(60 + rpmPerKn*sog + 1.5*noise)
	s.FuelRateLph = float32(15 + fuelK*sog*sog*sog)
	s.EngineTempC = float32(72 + 0.9*sog + 2*math.Sin(float64(tSec)/900+phase*6.28))
	// Fuel level: sawtooth from 95% down to 15% over a 30 to 60 hour cycle, then "refuel".
	cycle := (30 + 30*unit(h>>48)) * 3600
	frac := math.Mod(float64(tSec)+phase*cycle, cycle) / cycle
	s.FuelLevelPct = float32(95 - 80*frac)
}
