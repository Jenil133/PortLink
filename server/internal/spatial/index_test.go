package spatial

import (
	"math"
	"math/rand"
	"testing"

	"github.com/portlink/lens/server/internal/model"
)

// checkBox compares Candidates, filtered by the box, with brute force. It returns false if the
// index declined the box, which sends the caller to a full scan and is always correct.
func checkBox(t *testing.T, ix *Index, pts [][2]float64, b model.BBox) bool {
	t.Helper()
	got := map[uint32]bool{}
	if !ix.Candidates(b, func(m uint32) {
		if b.Contains(pts[m][0], pts[m][1]) {
			got[m] = true
		}
	}) {
		return false
	}
	for i, p := range pts {
		if b.Contains(p[0], p[1]) != got[uint32(i)] {
			t.Fatalf("box %+v: vessel %d at %v in box=%v, returned=%v", b, i, p, b.Contains(p[0], p[1]), got[uint32(i)])
		}
	}
	return true
}

func TestCandidatesEqualBruteForce(t *testing.T) {
	r := rand.New(rand.NewSource(7))
	ix := New()
	pts := make([][2]float64, 5000)
	for i := range pts {
		pts[i] = [2]float64{30 + r.Float64()*20, -130 + r.Float64()*15}
		ix.Move(uint32(i), pts[i][0], pts[i][1])
	}
	indexed := 0
	for q := 0; q < 50; q++ {
		w, h := 0.02+r.Float64()*r.Float64()*14, 0.02+r.Float64()*r.Float64()*14
		minLon, minLat := -130+r.Float64()*(15-w), 30+r.Float64()*(20-math.Min(h, 19.9))
		if checkBox(t, ix, pts, model.BBox{MinLon: minLon, MinLat: minLat, MaxLon: minLon + w, MaxLat: minLat + h}) {
			indexed++
		}
	}
	if indexed < 40 {
		t.Fatalf("only %d of 50 boxes used the index: the test proves too little", indexed)
	}
}

// Shapes that used to lose vessels: each box gets its own fleet, half of it on the box's edges.
func TestCandidatesAwkwardBoxes(t *testing.T) {
	r := rand.New(rand.NewSource(11))
	uni := func(lo, hi float64) float64 { return lo + r.Float64()*(hi-lo) }
	shapes := map[string]func() model.BBox{
		"west edge at -180": func() model.BBox {
			lat := uni(-70, 70)
			return model.BBox{MinLon: -180, MaxLon: -180 + uni(0.1, 20), MinLat: lat, MaxLat: lat + uni(0.1, 10)}
		},
		"east edge at 180": func() model.BBox {
			lat := uni(-70, 70)
			return model.BBox{MinLon: 180 - uni(0.1, 20), MaxLon: 180, MinLat: lat, MaxLat: lat + uni(0.1, 10)}
		},
		"just short of the antimeridian": func() model.BBox {
			lat, e := uni(-70, 70), uni(177, 179.95)
			return model.BBox{MinLon: e - uni(0.1, 8), MaxLon: e, MinLat: lat, MaxLat: lat + uni(0.1, 5)}
		},
		"very wide": func() model.BBox {
			lat, w := uni(-60, 60), uni(100, 359)
			lon := uni(-180, 180-w)
			return model.BBox{MinLon: lon, MaxLon: lon + w, MinLat: lat, MaxLat: lat + uni(0.05, 2)}
		},
		"very tall": func() model.BBox {
			h := uni(100, 165)
			lat, lon := uni(-85, 85-h), uni(-170, 170)
			return model.BBox{MinLon: lon, MaxLon: lon + uni(0.1, 3), MinLat: lat, MaxLat: lat + h}
		},
		"high latitude": func() model.BBox {
			lat, lon := uni(70, 88.9), uni(-170, 130)
			if r.Intn(2) == 0 {
				lat = -lat - 1
			}
			return model.BBox{MinLon: lon, MaxLon: lon + uni(0.2, 40), MinLat: lat, MaxLat: lat + uni(0.05, 1)}
		},
	}
	for name, box := range shapes {
		t.Run(name, func(t *testing.T) {
			for q := 0; q < 40; q++ {
				b := box()
				ix := New()
				pts := make([][2]float64, 400)
				for i := range pts {
					lat, lon := uni(b.MinLat, b.MaxLat), uni(b.MinLon, b.MaxLon)
					switch i % 6 {
					case 0:
						lat = b.MinLat
					case 1:
						lat = b.MaxLat
					case 2:
						lon = b.MinLon
					case 3:
						lon = b.MaxLon
					}
					pts[i] = [2]float64{lat, lon}
					ix.Move(uint32(i), lat, lon)
				}
				checkBox(t, ix, pts, b)
			}
		})
	}
}

func TestLargeBoxFallsBackToScan(t *testing.T) {
	ix := New()
	ix.Move(1, 37.8, -122.5)
	for _, b := range []model.BBox{
		{MinLon: -180, MinLat: -85, MaxLon: 180, MaxLat: 85}, // the world
		{MinLon: 170, MinLat: 0, MaxLon: 190, MaxLat: 1},     // past the antimeridian
		{MinLon: -100, MinLat: 0, MaxLon: 90, MaxLat: 2},     // wider than 180 degrees
		{MinLon: 0, MinLat: 88.8, MaxLon: 1, MaxLat: 90},     // at a pole
	} {
		if ix.Candidates(b, func(uint32) {}) {
			t.Fatalf("box %+v must fall back to a scan", b)
		}
	}
}

func TestRemove(t *testing.T) {
	ix := New()
	ix.Move(1, 37.8, -122.5)
	b := model.BBox{MinLon: -122.6, MinLat: 37.7, MaxLon: -122.4, MaxLat: 37.9}
	found := false
	ix.Candidates(b, func(m uint32) { found = found || m == 1 })
	if !found {
		t.Fatal("vessel not found before Remove")
	}
	ix.Remove(1)
	ix.Candidates(b, func(m uint32) {
		if m == 1 {
			t.Fatal("vessel still found after Remove")
		}
	})
	ix.Remove(1) // removing twice is harmless
}

func TestMoveBetweenCells(t *testing.T) {
	ix := New()
	ix.Move(1, 37.8, -122.5)
	ix.Move(1, 47.6, -122.3)
	here := model.BBox{MinLon: -122.6, MinLat: 37.7, MaxLon: -122.4, MaxLat: 37.9}
	ix.Candidates(here, func(m uint32) {
		if m == 1 {
			t.Fatal("vessel still found in its old cell")
		}
	})
	there := model.BBox{MinLon: -122.4, MinLat: 47.5, MaxLon: -122.2, MaxLat: 47.7}
	found := false
	ix.Candidates(there, func(m uint32) { found = found || m == 1 })
	if !found {
		t.Fatal("vessel not found in its new cell")
	}
}
