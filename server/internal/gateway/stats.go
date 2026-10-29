package gateway

import (
	"cmp"
	"encoding/json"
	"math"
	"net/http"
	"slices"
	"time"

	lensv1 "github.com/portlink/lens/server/internal/gen/lens/v1"
)

// StatsResponse is what GET /api/stats returns.
type StatsResponse struct {
	ServerTimeMs  int64         `json:"server_time_ms"`
	Vessels       int           `json:"vessels"`
	SamplesTotal  uint64        `json:"samples_total"`
	SamplesPerSec float64       `json:"samples_per_sec"`
	Clients       []ClientStats `json:"clients"`
}

type ClientStats struct {
	ID        uint64  `json:"id"`
	Mode      string  `json:"mode"`   // "live" | "history" | "none"
	Render    string  `json:"render"` // "lens" | "clustered" | "firehose"
	Zoom      float32 `json:"zoom"`
	InView    uint32  `json:"in_view"`
	BytesOut  uint64  `json:"bytes_out"`
	FramesOut uint64  `json:"frames_out"`
	Dropped   uint32  `json:"dropped"`
	SlowMs    int     `json:"slow_ms"`
}

func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	resp := StatsResponse{
		ServerTimeMs:  time.Now().UnixMilli(),
		Vessels:       s.world.Len(),
		SamplesTotal:  s.samples.Load(),
		SamplesPerSec: math.Float64frombits(s.samplesSec.Load()),
		Clients:       []ClientStats{},
	}
	s.mu.Lock()
	for c := range s.clients {
		resp.Clients = append(resp.Clients, c.stats())
	}
	s.mu.Unlock()
	slices.SortFunc(resp.Clients, func(a, b ClientStats) int { return cmp.Compare(a.ID, b.ID) })
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// stats runs outside the client's goroutines, so it reads atomics and the subscription only and
// derives the render mode from the subscription. The writer-owned c.render would be a data race.
func (c *client) stats() ClientStats {
	c.mu.Lock()
	sub := c.sub // never changed once published, so it stays safe to read after unlocking
	c.mu.Unlock()
	st := ClientStats{
		ID: c.id, Mode: "none", Render: "lens",
		InView: c.inView.Load(), BytesOut: c.bytesOut.Load(), FramesOut: c.framesOut.Load(),
		Dropped: c.dropped.Load(), SlowMs: int(c.slow.Milliseconds()),
	}
	if sub == nil {
		return st
	}
	st.Zoom = sub.Zoom
	st.Mode = "live"
	if sub.Mode == lensv1.Mode_MODE_HISTORY {
		st.Mode = "history"
	}
	switch renderOf(sub, c.srv.cfg.ClusterZoom) {
	case renderClustered:
		st.Render = "clustered"
	case renderFirehose:
		st.Render = "firehose"
	}
	return st
}
