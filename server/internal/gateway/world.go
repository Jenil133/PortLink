package gateway

import (
	"sync"

	"github.com/portlink/lens/server/internal/model"
)

// World holds the latest state of every vessel. One writer (the NATS callback), many readers.
type World struct {
	mu      sync.RWMutex
	vessels map[uint32]*model.VesselState
}

func NewWorld() *World { return &World{vessels: make(map[uint32]*model.VesselState)} }

// Upsert ignores samples older than what we already have, so out-of-order delivery is safe.
func (w *World) Upsert(s model.VesselState) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if cur, ok := w.vessels[s.MMSI]; ok {
		if s.TsMs < cur.TsMs {
			return
		}
		*cur = s
		return
	}
	cp := s
	w.vessels[s.MMSI] = &cp
}

// Query appends copies of every vessel inside the box to dst. Phase 1 scans linearly.
// Phase 2 replaces the scan with an H3 index but keeps this signature.
func (w *World) Query(b model.BBox, all bool, dst []model.VesselState) []model.VesselState {
	w.mu.RLock()
	defer w.mu.RUnlock()
	for _, v := range w.vessels {
		if all || b.Contains(v.Lat, v.Lon) {
			dst = append(dst, *v)
		}
	}
	return dst
}

func (w *World) Len() int {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return len(w.vessels)
}
