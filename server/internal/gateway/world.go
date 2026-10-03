package gateway

import (
	"sync"

	"github.com/portlink/lens/server/internal/model"
	"github.com/portlink/lens/server/internal/spatial"
)

// World holds the latest state of every vessel. One writer (the NATS callback), many readers.
type World struct {
	mu      sync.RWMutex
	vessels map[uint32]*model.VesselState
	ix      *spatial.Index
}

func NewWorld() *World {
	return &World{vessels: make(map[uint32]*model.VesselState), ix: spatial.New()}
}

// Upsert ignores samples older than what we already have, so out-of-order delivery is safe.
func (w *World) Upsert(s model.VesselState) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if cur, ok := w.vessels[s.MMSI]; ok {
		if s.TsMs < cur.TsMs {
			return // out-of-order sample
		}
		*cur = s
	} else {
		cp := s
		w.vessels[s.MMSI] = &cp
	}
	w.ix.Move(s.MMSI, s.Lat, s.Lon)
}

// Query appends copies of the vessels inside the box.
func (w *World) Query(b model.BBox, all bool, dst []model.VesselState) []model.VesselState {
	w.mu.RLock()
	defer w.mu.RUnlock()
	if !all {
		ok := w.ix.Candidates(b, func(mmsi uint32) {
			if v := w.vessels[mmsi]; v != nil && b.Contains(v.Lat, v.Lon) {
				dst = append(dst, *v)
			}
		})
		if ok {
			return dst
		}
	}
	for _, v := range w.vessels { // full scan: all=true or the box was too large for the index
		if all || b.Contains(v.Lat, v.Lon) {
			dst = append(dst, *v)
		}
	}
	return dst
}

// EvictStale removes vessels that have not reported since cutoffMs. Returns how many were removed.
func (w *World) EvictStale(cutoffMs int64) int {
	w.mu.Lock()
	defer w.mu.Unlock()
	n := 0
	for mmsi, v := range w.vessels {
		if v.TsMs < cutoffMs {
			delete(w.vessels, mmsi)
			w.ix.Remove(mmsi)
			n++
		}
	}
	return n
}

func (w *World) Len() int {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return len(w.vessels)
}
