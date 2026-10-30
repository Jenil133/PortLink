package gateway

import (
	"testing"
	"time"
)

func TestTierForZoom(t *testing.T) {
	for _, c := range []struct {
		zoom float64
		want string
	}{{3, "samples_10m"}, {6.9, "samples_10m"}, {7, "samples_1m"}, {10.9, "samples_1m"}, {11, "samples_1s"}, {18, "samples_1s"}} {
		if got := tierForZoom(c.zoom).view; got != c.want {
			t.Errorf("zoom %v: got %s, want %s", c.zoom, got, c.want)
		}
	}
}

func TestTierForWindow(t *testing.T) {
	for _, c := range []struct {
		window time.Duration
		want   string
	}{
		{10 * time.Minute, "samples_1s"},
		{6 * time.Hour, "samples_1s"},
		{7 * time.Hour, "samples_1m"},
		{20 * 24 * time.Hour, "samples_10m"},
	} {
		if got := tierForWindow(c.window).view; got != c.want {
			t.Errorf("window %s: got %s, want %s", c.window, got, c.want)
		}
	}
}
