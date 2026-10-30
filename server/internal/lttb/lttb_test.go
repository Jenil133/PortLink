package lttb

import (
	"math"
	"testing"
)

func TestIndices(t *testing.T) {
	n := 10000
	xs, ys := make([]float64, n), make([]float64, n)
	for i := range xs {
		xs[i] = float64(i)
		ys[i] = math.Sin(float64(i) / 50)
	}
	idx := Indices(xs, ys, 500)
	if len(idx) != 500 || idx[0] != 0 || idx[len(idx)-1] != n-1 {
		t.Fatalf("len=%d first=%d last=%d", len(idx), idx[0], idx[len(idx)-1])
	}
	for i := 1; i < len(idx); i++ {
		if idx[i] <= idx[i-1] {
			t.Fatalf("indices not strictly increasing at %d", i)
		}
	}
	if got := Indices(xs[:10], ys[:10], 500); len(got) != 10 {
		t.Fatalf("short input should be returned whole, got %d", len(got))
	}
	if got := Indices(nil, nil, 500); len(got) != 0 {
		t.Fatalf("empty input should stay empty, got %d", len(got))
	}
	// A single spike must survive downsampling: that is what LTTB is for.
	ys[5000] = 100
	found := false
	for _, i := range Indices(xs, ys, 500) {
		found = found || i == 5000
	}
	if !found {
		t.Fatal("spike was dropped")
	}
}
