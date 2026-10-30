package gateway

import (
	"testing"

	lensv1 "github.com/portlink/lens/server/internal/gen/lens/v1"
	"github.com/portlink/lens/server/internal/model"
)

func base() model.VesselState {
	return model.VesselState{MMSI: 1, TsMs: 1000, Lat: 37.8, Lon: -122.5, Sog: 12, Cog: 90, Heading: 90, Name: "A"}
}

func TestFirstSightSendsEverything(t *testing.T) {
	v := base()
	d, _ := diffVessel(1, nil, quantize(&v), posThresholdE6(10), false, false)
	if d == nil || d.LatE6 == nil || d.Name == nil || d.EngineRpm != nil {
		t.Fatalf("unexpected first delta: %v", d)
	}
}

func TestNoChangeNoDelta(t *testing.T) {
	v := base()
	_, st := diffVessel(1, nil, quantize(&v), 100, false, false)
	if d, _ := diffVessel(1, &st, quantize(&v), 100, false, false); d != nil {
		t.Fatalf("expected nil, got %v", d)
	}
}

// Sub-threshold moves must accumulate against the last SENT position.
func TestDriftAccumulates(t *testing.T) {
	v := base()
	thr := int32(100) // microdegrees
	_, st := diffVessel(1, nil, quantize(&v), thr, false, false)
	sent := 0
	for i := 0; i < 10; i++ {
		v.Lat += 0.00003 // 30 microdegrees per step
		var d *lensv1.VesselDelta
		d, st = diffVessel(1, &st, quantize(&v), thr, false, false)
		if d != nil {
			sent++
		}
	}
	if sent < 2 || sent > 3 {
		t.Fatalf("expected 2 or 3 position updates for 300 microdegrees of drift, got %d", sent)
	}
}

func TestZoomThreshold(t *testing.T) {
	if posThresholdE6(5) <= posThresholdE6(14) {
		t.Fatal("threshold must shrink as zoom grows")
	}
	if posThresholdE6(22) < 1 {
		t.Fatal("threshold must never be below 1")
	}
}

func TestTelemetryOnlyWhenSelected(t *testing.T) {
	v := base()
	v.EngineRPM = 100
	_, st := diffVessel(1, nil, quantize(&v), 100, false, false)
	v.EngineRPM = 140
	if d, _ := diffVessel(1, &st, quantize(&v), 100, false, false); d != nil {
		t.Fatal("telemetry change must not be sent for an unselected vessel")
	}
	d, _ := diffVessel(1, &st, quantize(&v), 100, true, false)
	if d == nil || d.EngineRpm == nil || d.FuelLevelDpct == nil {
		t.Fatal("selecting a vessel must send all telemetry fields once")
	}
}

func TestCourseWrapAround(t *testing.T) {
	v := base()
	v.Cog = 359
	_, st := diffVessel(1, nil, quantize(&v), 100, false, false)
	v.Cog = 1 // 2 degrees across north, below the 3 degree threshold
	if d, _ := diffVessel(1, &st, quantize(&v), 100, false, false); d != nil {
		t.Fatal("359 -> 1 degrees is a 2 degree change, not 358")
	}
}

func TestFirehoseSendsFullOnNewSample(t *testing.T) {
	v := base()
	_, st := diffVessel(1, nil, quantize(&v), 1, true, true)
	if d, _ := diffVessel(1, &st, quantize(&v), 1, true, true); d != nil {
		t.Fatal("same sample must not be resent")
	}
	v.TsMs += 1000
	d, _ := diffVessel(1, &st, quantize(&v), 1, true, true)
	if d == nil || d.Name == nil || d.EngineRpm == nil {
		t.Fatal("a new sample in firehose mode must carry every field")
	}
}

func TestSampleTimeIsRememberedInLensMode(t *testing.T) {
	v := base()
	_, st := diffVessel(1, nil, quantize(&v), 100, false, false)
	v.TsMs += 1000
	d, next := diffVessel(1, &st, quantize(&v), 100, false, false)
	if d != nil || next.tsMs != v.TsMs {
		t.Fatalf("delta %v, remembered ts %d, want no delta and ts %d", d, next.tsMs, v.TsMs)
	}
}

// Every angle on the wire stays on the circle, whatever the source sent.
func TestAnglesStayInRange(t *testing.T) {
	for _, deg := range []float32{-10, 0, 1, 180, 359, 359.4, 359.6, 359.96, 360, 361, 511, 720} {
		v := base()
		v.Heading, v.Cog = deg, deg
		q := quantize(&v)
		if q.headingDeg > 359 {
			t.Fatalf("heading %v went out as %d", deg, q.headingDeg)
		}
		if q.cogDdeg > 3599 {
			t.Fatalf("course %v went out as %d", deg, q.cogDdeg)
		}
	}
	v := base()
	v.Heading = 359.6
	if q := quantize(&v); q.headingDeg != 0 {
		t.Fatalf("359.6 rounds to 360, which is 0, got %d", q.headingDeg)
	}
}
