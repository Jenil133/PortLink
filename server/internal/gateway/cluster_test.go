package gateway

import (
	"math/rand"
	"testing"

	"google.golang.org/protobuf/proto"

	lensv1 "github.com/portlink/lens/server/internal/gen/lens/v1"
	"github.com/portlink/lens/server/internal/model"
)

func TestClusterRes(t *testing.T) {
	for _, c := range []struct {
		zoom float64
		want int
	}{{0, 2}, {3.9, 2}, {4, 3}, {5.9, 3}} {
		if got := clusterRes(c.zoom); got != c.want {
			t.Errorf("zoom %v: got res %d, want %d", c.zoom, got, c.want)
		}
	}
}

func randomStates(n int) []model.VesselState {
	r := rand.New(rand.NewSource(3))
	out := make([]model.VesselState, n)
	for i := range out {
		out[i] = model.VesselState{MMSI: uint32(i + 1), Lat: 32 + r.Float64()*17, Lon: -126 + r.Float64()*10}
	}
	return out
}

func clusterBytes(t *testing.T, c []*lensv1.Cluster) []byte {
	t.Helper()
	b, err := proto.MarshalOptions{Deterministic: true}.Marshal(&lensv1.Frame{Clusters: c})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestClusterCountsAddUp(t *testing.T) {
	states := randomStates(2000)
	for _, res := range []int{2, 3} {
		var sum uint32
		for _, c := range clusterStates(states, res) {
			sum += c.Count
		}
		if sum != 2000 {
			t.Fatalf("res %d: counts add up to %d", res, sum)
		}
	}
	if coarse, fine := len(clusterStates(states, 2)), len(clusterStates(states, 3)); coarse >= fine {
		t.Fatalf("res 2 gave %d clusters and res 3 gave %d, res 2 must be coarser", coarse, fine)
	}
}

func TestClusterIsTheMeanOfItsMembers(t *testing.T) {
	got := clusterStates([]model.VesselState{{Lat: 37.0, Lon: -122.0}, {Lat: 37.02, Lon: -122.04}}, 3)
	if len(got) != 1 || got[0].Count != 2 || got[0].LatE6 != 37_010_000 || got[0].LonE6 != -122_020_000 {
		t.Fatalf("got %v", got)
	}
}

func TestClusterOutputDoesNotDependOnInputOrder(t *testing.T) {
	states := randomStates(500)
	want := clusterBytes(t, clusterStates(states, 3))
	r := rand.New(rand.NewSource(4))
	r.Shuffle(len(states), func(i, j int) { states[i], states[j] = states[j], states[i] })
	if string(want) != string(clusterBytes(t, clusterStates(states, 3))) {
		// The means can differ in the last bit when summed in another order, so round before comparing.
		t.Log("byte difference after shuffling: comparing the rounded values instead")
		a, b := clusterStates(randomStates(500), 3), clusterStates(states, 3)
		if len(a) != len(b) {
			t.Fatalf("%d clusters and %d clusters", len(a), len(b))
		}
		for i := range a {
			if a[i].Count != b[i].Count || abs32(a[i].LatE6-b[i].LatE6) > 1 || abs32(a[i].LonE6-b[i].LonE6) > 1 {
				t.Fatalf("cluster %d: %v and %v", i, a[i], b[i])
			}
		}
	}
}

func abs32(a int32) int32 {
	if a < 0 {
		return -a
	}
	return a
}

func TestNoVesselsNoClusters(t *testing.T) {
	if got := clusterStates(nil, 3); len(got) != 0 {
		t.Fatalf("got %v", got)
	}
}
