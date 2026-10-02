package gateway

import (
	"math"

	"google.golang.org/protobuf/proto"

	lensv1 "github.com/portlink/lens/server/internal/gen/lens/v1"
	"github.com/portlink/lens/server/internal/model"
)

// qState is what one client currently holds for one vessel, in wire units.
type qState struct {
	gen                             uint32 // last diff pass that saw this vessel (used to find removals)
	latE6, lonE6                    int32
	sogDkn, cogDdeg, headingDeg     uint32
	name                            string
	vtype                           uint32
	rpm, fuelDlph, tempDc, fuelDpct uint32
	hasTelem                        bool // telemetry has been sent for this vessel
}

func quantize(v *model.VesselState) qState {
	r := func(f float32, scale float64) uint32 { return uint32(math.Max(0, math.Round(float64(f)*scale))) }
	return qState{
		latE6: int32(math.Round(v.Lat * 1e6)), lonE6: int32(math.Round(v.Lon * 1e6)),
		sogDkn: r(v.Sog, 10), cogDdeg: r(v.Cog, 10) % 3600, headingDeg: r(v.Heading, 1) % 360,
		name: v.Name, vtype: v.VesselType,
		rpm: r(v.EngineRPM, 1), fuelDlph: r(v.FuelRateLph, 10), tempDc: r(v.EngineTempC, 10), fuelDpct: r(v.FuelLevelPct, 10),
	}
}

// posThresholdE6 is half a screen pixel at this zoom, in microdegrees.
// deck.gl/MapLibre use 512 px tiles, so one pixel = 360 / (512 * 2^zoom) degrees of longitude.
func posThresholdE6(zoom float64) int32 {
	t := 0.5 * 360e6 / (512 * math.Pow(2, zoom))
	return int32(math.Min(2e6, math.Max(1, t)))
}

const (
	thrSogDkn   = 5  // 0.5 kn
	thrCogDdeg  = 30 // 3 degrees
	thrHeading  = 3
	thrRPM      = 1
	thrFuelDlph = 5 // 0.5 L/h
	thrTempDc   = 2 // 0.2 C
	thrFuelDpct = 1 // 0.1 %
)

func absI32(a int32) int32 {
	if a < 0 {
		return -a
	}
	return a
}

func absDiff(a, b uint32) uint32 {
	if a > b {
		return a - b
	}
	return b - a
}

// angDiff is the circular difference for angles stored modulo `full` (3600 for decidegrees, 360 for degrees).
func angDiff(a, b, full uint32) uint32 {
	d := absDiff(a, b)
	if d > full/2 {
		return full - d
	}
	return d
}

// diffVessel compares what the client holds (prev, nil if it has never seen the vessel) with the
// current state. It returns the delta to send (nil if nothing crossed a threshold) and the state
// to remember. Fields that were NOT sent keep their previous value in the remembered state, so
// slow drift accumulates and is eventually sent. Comparing against the last sample instead would
// let a slowly moving vessel never update.
func diffVessel(mmsi uint32, prev *qState, cur qState, posThr int32, telem bool) (*lensv1.VesselDelta, qState) {
	if prev == nil {
		d := &lensv1.VesselDelta{
			Mmsi: mmsi, LatE6: proto.Int32(cur.latE6), LonE6: proto.Int32(cur.lonE6),
			SogDkn: proto.Uint32(cur.sogDkn), CogDdeg: proto.Uint32(cur.cogDdeg), HeadingDeg: proto.Uint32(cur.headingDeg),
			Name: proto.String(cur.name), VesselType: proto.Uint32(cur.vtype),
		}
		if telem {
			d.EngineRpm, d.FuelRateDlph = proto.Uint32(cur.rpm), proto.Uint32(cur.fuelDlph)
			d.EngineTempDc, d.FuelLevelDpct = proto.Uint32(cur.tempDc), proto.Uint32(cur.fuelDpct)
		}
		cur.hasTelem = telem
		return d, cur
	}

	next := *prev
	var d *lensv1.VesselDelta
	out := func() *lensv1.VesselDelta { // allocate only when something actually changed
		if d == nil {
			d = &lensv1.VesselDelta{Mmsi: mmsi}
		}
		return d
	}
	// Position: lat and lon always travel together so the client never draws a half-updated point.
	if absI32(cur.latE6-prev.latE6) >= posThr || absI32(cur.lonE6-prev.lonE6) >= posThr {
		out().LatE6, out().LonE6 = proto.Int32(cur.latE6), proto.Int32(cur.lonE6)
		next.latE6, next.lonE6 = cur.latE6, cur.lonE6
	}
	if absDiff(cur.sogDkn, prev.sogDkn) >= thrSogDkn {
		out().SogDkn = proto.Uint32(cur.sogDkn)
		next.sogDkn = cur.sogDkn
	}
	if angDiff(cur.cogDdeg, prev.cogDdeg, 3600) >= thrCogDdeg {
		out().CogDdeg = proto.Uint32(cur.cogDdeg)
		next.cogDdeg = cur.cogDdeg
	}
	if angDiff(cur.headingDeg, prev.headingDeg, 360) >= thrHeading {
		out().HeadingDeg = proto.Uint32(cur.headingDeg)
		next.headingDeg = cur.headingDeg
	}
	if cur.name != prev.name {
		out().Name = proto.String(cur.name)
		next.name = cur.name
	}
	if cur.vtype != prev.vtype {
		out().VesselType = proto.Uint32(cur.vtype)
		next.vtype = cur.vtype
	}
	if telem { // telemetry only for the selected vessel
		first := !prev.hasTelem
		if first || absDiff(cur.rpm, prev.rpm) >= thrRPM {
			out().EngineRpm = proto.Uint32(cur.rpm)
			next.rpm = cur.rpm
		}
		if first || absDiff(cur.fuelDlph, prev.fuelDlph) >= thrFuelDlph {
			out().FuelRateDlph = proto.Uint32(cur.fuelDlph)
			next.fuelDlph = cur.fuelDlph
		}
		if first || absDiff(cur.tempDc, prev.tempDc) >= thrTempDc {
			out().EngineTempDc = proto.Uint32(cur.tempDc)
			next.tempDc = cur.tempDc
		}
		if first || absDiff(cur.fuelDpct, prev.fuelDpct) >= thrFuelDpct {
			out().FuelLevelDpct = proto.Uint32(cur.fuelDpct)
			next.fuelDpct = cur.fuelDpct
		}
		next.hasTelem = true
	} else {
		next.hasTelem = false // if the vessel is selected again later, resend all four fields
	}
	return d, next
}
