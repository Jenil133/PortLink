package spatial

import (
	"math"

	"github.com/uber/h3-go/v4"

	"github.com/portlink/lens/server/internal/model"
)

const (
	Res         = 4      // H3 resolution of the live index
	cellAreaKm2 = 1770.0 // approximate average res-4 cell area
	maxCells    = 3000   // above this, enumerating cells costs more than scanning every vessel
)

// Index maps H3 cells to the vessels inside them. It is NOT thread safe, World's mutex guards it.
type Index struct {
	cells map[h3.Cell]map[uint32]struct{}
	where map[uint32]h3.Cell
}

func New() *Index {
	return &Index{cells: map[h3.Cell]map[uint32]struct{}{}, where: map[uint32]h3.Cell{}}
}

// Move records the vessel's current cell. Most calls are a no-op because the cell did not change.
func (ix *Index) Move(mmsi uint32, lat, lon float64) {
	cell, err := h3.LatLngToCell(h3.NewLatLng(lat, lon), Res)
	if err != nil {
		return
	}
	if old, ok := ix.where[mmsi]; ok {
		if old == cell {
			return
		}
		ix.removeFrom(old, mmsi)
	}
	set := ix.cells[cell]
	if set == nil {
		set = map[uint32]struct{}{}
		ix.cells[cell] = set
	}
	set[mmsi] = struct{}{}
	ix.where[mmsi] = cell
}

func (ix *Index) Remove(mmsi uint32) {
	if old, ok := ix.where[mmsi]; ok {
		ix.removeFrom(old, mmsi)
		delete(ix.where, mmsi)
	}
}

func (ix *Index) removeFrom(cell h3.Cell, mmsi uint32) {
	set := ix.cells[cell]
	delete(set, mmsi)
	if len(set) == 0 {
		delete(ix.cells, cell)
	}
}

// Candidates calls visit for every vessel in cells that may overlap the box.
// It returns false when the box is too large (or crosses the antimeridian) and the caller
// should scan all vessels instead. Candidates are a superset: the caller still checks the exact box.
func (ix *Index) Candidates(b model.BBox, visit func(mmsi uint32)) bool {
	if b.MinLon < -180 || b.MaxLon > 180 {
		return false
	}
	midLat := (b.MinLat + b.MaxLat) / 2
	cosLat := math.Max(0.2, math.Cos(midLat*math.Pi/180))
	wKm := (b.MaxLon - b.MinLon) * 111.32 * cosLat
	hKm := (b.MaxLat - b.MinLat) * 110.57
	if wKm*hKm/cellAreaKm2 > maxCells {
		return false
	}
	// PolygonToCells returns cells whose CENTER is inside the polygon. Padding the box by about
	// one cell diameter (0.5 degrees) guarantees every cell that touches the real box is included.
	padLat, padLon := 0.5, 0.5/cosLat
	minLat, maxLat := math.Max(b.MinLat-padLat, -89), math.Min(b.MaxLat+padLat, 89)
	minLon, maxLon := math.Max(b.MinLon-padLon, -180), math.Min(b.MaxLon+padLon, 180)
	loop := h3.GeoLoop{
		h3.NewLatLng(minLat, minLon), h3.NewLatLng(minLat, maxLon),
		h3.NewLatLng(maxLat, maxLon), h3.NewLatLng(maxLat, minLon),
	}
	cells, err := h3.PolygonToCells(h3.GeoPolygon{GeoLoop: loop}, Res)
	if err != nil {
		return false
	}
	for _, c := range cells {
		for mmsi := range ix.cells[c] {
			visit(mmsi)
		}
	}
	return true
}
