package gateway

import (
	"math"
	"sort"

	"github.com/uber/h3-go/v4"

	lensv1 "github.com/portlink/lens/server/internal/gen/lens/v1"
	"github.com/portlink/lens/server/internal/model"
)

// clusterRes: coarser cells when zoomed further out. Res 2 cells are about 300 km across, res 3 about 120 km.
func clusterRes(zoom float64) int {
	if zoom < 4 {
		return 2
	}
	return 3
}

// clusterStates groups vessels by H3 cell and returns one Cluster per cell, positioned at the
// mean of its members (looks better than the cell centre, which may be on land).
// Output is sorted by cell id so identical input gives identical bytes.
func clusterStates(states []model.VesselState, res int) []*lensv1.Cluster {
	type acc struct {
		lat, lon float64
		n        uint32
	}
	m := map[h3.Cell]*acc{}
	for i := range states {
		cell, err := h3.LatLngToCell(h3.NewLatLng(states[i].Lat, states[i].Lon), res)
		if err != nil {
			continue
		}
		a := m[cell]
		if a == nil {
			a = &acc{}
			m[cell] = a
		}
		a.lat += states[i].Lat
		a.lon += states[i].Lon
		a.n++
	}
	cells := make([]h3.Cell, 0, len(m))
	for c := range m {
		cells = append(cells, c)
	}
	sort.Slice(cells, func(i, j int) bool { return cells[i] < cells[j] })
	out := make([]*lensv1.Cluster, 0, len(cells))
	for _, c := range cells {
		a := m[c]
		out = append(out, &lensv1.Cluster{
			LatE6: int32(math.Round(a.lat / float64(a.n) * 1e6)),
			LonE6: int32(math.Round(a.lon / float64(a.n) * 1e6)),
			Count: a.n,
		})
	}
	return out
}
