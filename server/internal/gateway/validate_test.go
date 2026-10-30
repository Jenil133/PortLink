package gateway

import (
	"math"
	"testing"

	lensv1 "github.com/portlink/lens/server/internal/gen/lens/v1"
)

func goodBox() *lensv1.BBox { return &lensv1.BBox{MinLon: -123, MinLat: 37, MaxLon: -122, MaxLat: 38} }

func TestNormalizeRefuses(t *testing.T) {
	nan := float32(math.NaN())
	for name, sub := range map[string]*lensv1.Subscribe{
		"nil bbox":             {},
		"inverted longitude":   {Bbox: &lensv1.BBox{MinLon: -122, MinLat: 37, MaxLon: -123, MaxLat: 38}},
		"inverted latitude":    {Bbox: &lensv1.BBox{MinLon: -123, MinLat: 38, MaxLon: -122, MaxLat: 37}},
		"empty box":            {Bbox: &lensv1.BBox{MinLon: -123, MinLat: 37, MaxLon: -123, MaxLat: 37}},
		"latitude past 90":     {Bbox: &lensv1.BBox{MinLon: -123, MinLat: 37, MaxLon: -122, MaxLat: 91}},
		"longitude past 360":   {Bbox: &lensv1.BBox{MinLon: -400, MinLat: 37, MaxLon: -122, MaxLat: 38}},
		"NaN in bbox":          {Bbox: &lensv1.BBox{MinLon: math.NaN(), MinLat: 37, MaxLon: -122, MaxLat: 38}},
		"infinity in bbox":     {Bbox: &lensv1.BBox{MinLon: math.Inf(-1), MinLat: 37, MaxLon: -122, MaxLat: 38}},
		"NaN zoom":             {Bbox: goodBox(), Zoom: nan},
		"history no cursor":    {Bbox: goodBox(), Mode: lensv1.Mode_MODE_HISTORY},
		"history cursor below": {Bbox: goodBox(), Mode: lensv1.Mode_MODE_HISTORY, CursorMs: -1},
		"unknown mode":         {Bbox: goodBox(), Mode: lensv1.Mode(99)},
	} {
		if normalize(sub, 6) == "" {
			t.Errorf("%s: must be refused", name)
		}
	}
}

func TestNormalizeFillsDefaults(t *testing.T) {
	sixHours := int64(6 * 3600 * 1000)
	for _, c := range []struct {
		name  string
		in    *lensv1.Subscribe
		check func(*lensv1.Subscribe) bool
	}{
		{"mode defaults to live", &lensv1.Subscribe{}, func(s *lensv1.Subscribe) bool { return s.Mode == lensv1.Mode_MODE_LIVE }},
		{"zoom clamps up", &lensv1.Subscribe{Zoom: -3}, func(s *lensv1.Subscribe) bool { return s.Zoom == 0 }},
		{"zoom clamps down", &lensv1.Subscribe{Zoom: 40}, func(s *lensv1.Subscribe) bool { return s.Zoom == 22 }},
		{"negative rate is paused", &lensv1.Subscribe{PlaybackRate: -2}, func(s *lensv1.Subscribe) bool { return s.PlaybackRate == 0 }},
		{"NaN rate is paused", &lensv1.Subscribe{PlaybackRate: float32(math.NaN())}, func(s *lensv1.Subscribe) bool { return s.PlaybackRate == 0 }},
		{"rate clamps", &lensv1.Subscribe{PlaybackRate: 9999}, func(s *lensv1.Subscribe) bool { return s.PlaybackRate == 600 }},
		{"oversized window clamps", &lensv1.Subscribe{SeriesWindowMs: 10 * sixHours}, func(s *lensv1.Subscribe) bool { return s.SeriesWindowMs == sixHours }},
		{"negative window is off", &lensv1.Subscribe{SeriesWindowMs: -1}, func(s *lensv1.Subscribe) bool { return s.SeriesWindowMs == 0 }},
		{"series points 0 becomes 600", &lensv1.Subscribe{SeriesMaxPoints: 0}, func(s *lensv1.Subscribe) bool { return s.SeriesMaxPoints == 600 }},
		{"series points 10 becomes 50", &lensv1.Subscribe{SeriesMaxPoints: 10}, func(s *lensv1.Subscribe) bool { return s.SeriesMaxPoints == 50 }},
		{"series points 99999 becomes 2000", &lensv1.Subscribe{SeriesMaxPoints: 99999}, func(s *lensv1.Subscribe) bool { return s.SeriesMaxPoints == 2000 }},
		{"firehose stays on in live mode", &lensv1.Subscribe{Firehose: true, Mode: lensv1.Mode_MODE_LIVE}, func(s *lensv1.Subscribe) bool { return s.Firehose }},
		{"firehose is cleared in history mode", &lensv1.Subscribe{Firehose: true, Mode: lensv1.Mode_MODE_HISTORY, CursorMs: 5}, func(s *lensv1.Subscribe) bool { return !s.Firehose }},
	} {
		c.in.Bbox = goodBox()
		if reason := normalize(c.in, 6); reason != "" {
			t.Errorf("%s: refused: %s", c.name, reason)
			continue
		}
		if !c.check(c.in) {
			t.Errorf("%s: got %v", c.name, c.in)
		}
	}
}
