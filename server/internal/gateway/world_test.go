package gateway

import (
	"testing"

	"github.com/portlink/lens/server/internal/model"
)

func TestWorldIgnoresOlderSamples(t *testing.T) {
	w := NewWorld()
	w.Upsert(model.VesselState{MMSI: 1, TsMs: 100, Lat: 37, Lon: -122})
	w.Upsert(model.VesselState{MMSI: 1, TsMs: 50, Lat: 40, Lon: -125})
	got := w.Query(model.BBox{}, true, nil)
	if len(got) != 1 || got[0].Lat != 37 {
		t.Fatalf("got %v", got)
	}
}

func TestWorldQuery(t *testing.T) {
	w := NewWorld()
	w.Upsert(model.VesselState{MMSI: 1, TsMs: 1, Lat: 37, Lon: -122})
	w.Upsert(model.VesselState{MMSI: 2, TsMs: 1, Lat: 47, Lon: -125})
	if got := w.Query(model.BBox{MinLon: -123, MinLat: 36, MaxLon: -121, MaxLat: 38}, false, nil); len(got) != 1 || got[0].MMSI != 1 {
		t.Fatalf("box query: %v", got)
	}
	if got := w.Query(model.BBox{MinLon: -123, MinLat: 36, MaxLon: -121, MaxLat: 38}, true, nil); len(got) != 2 {
		t.Fatalf("all=true must ignore the box: %v", got)
	}
	if got := w.Query(model.BBox{MinLon: -180, MinLat: -85, MaxLon: 180, MaxLat: 85}, false, nil); len(got) != 2 {
		t.Fatalf("a box too large for the index must still answer: %v", got)
	}
}

func TestWorldEviction(t *testing.T) {
	w := NewWorld()
	w.Upsert(model.VesselState{MMSI: 1, TsMs: 100, Lat: 37, Lon: -122})
	w.Upsert(model.VesselState{MMSI: 2, TsMs: 200, Lat: 37.1, Lon: -122})
	if n := w.EvictStale(150); n != 1 || w.Len() != 1 {
		t.Fatalf("evicted %d, %d left", n, w.Len())
	}
	if got := w.Query(model.BBox{MinLon: -123, MinLat: 36, MaxLon: -121, MaxLat: 38}, false, nil); len(got) != 1 || got[0].MMSI != 2 {
		t.Fatalf("the index still holds the evicted vessel: %v", got)
	}
}
