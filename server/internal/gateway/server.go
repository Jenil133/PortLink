package gateway

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"

	"github.com/portlink/lens/server/internal/model"
	"github.com/portlink/lens/server/internal/store"
)

// historyStore is the part of *store.Store the gateway needs. An interface so tests can fake it.
type historyStore interface {
	AsOf(ctx context.Context, view string, from, to time.Time, b model.BBox) ([]model.VesselState, error)
	SeriesRows(ctx context.Context, view string, mmsi int64, from, to time.Time) ([]store.SeriesRow, error)
	HistoryStart(ctx context.Context, hours int) (time.Time, error)
}

type Config struct {
	Addr             string
	AllowedOrigins   []string      // host:port patterns passed to websocket.AcceptOptions.OriginPatterns
	LiveTick         time.Duration // from LIVE_TICK_MS, default 100 ms, clamped to [20 ms, 1 s]
	HistoryHours     int           // from HISTORY_HOURS, default 6
	ClusterZoom      float64       // from CLUSTER_ZOOM, default 6: below this zoom the server sends clusters
	MaxClients       int           // from MAX_CLIENTS, default 200
	AllowDebugParams bool          // from ALLOW_DEBUG_PARAMS: honours /ws?slow_ms=N
}

type Server struct {
	ctx   context.Context // root context: cancelled on shutdown, closes every client
	cfg   Config
	world *World
	st    historyStore
	live  *liveSource

	mu      sync.Mutex
	clients map[*client]struct{}
	slots   int // connections holding a place, taken before the upgrade so a burst cannot overshoot
	nextID  uint64

	samples    atomic.Uint64 // total samples received from NATS
	samplesSec atomic.Uint64 // math.Float64bits of the last 1 s rate

	probeTimeout time.Duration // a field so that a test need not wait a minute
}

func NewServer(ctx context.Context, cfg Config, world *World, st historyStore) *Server {
	s := &Server{
		ctx: ctx, cfg: cfg, world: world, st: st, live: &liveSource{world: world},
		clients: make(map[*client]struct{}), probeTimeout: defaultProbeTimeout,
	}
	go s.rateLoop()
	return s
}

// CountSample is called from the NATS callback in main.
func (s *Server) CountSample() { s.samples.Add(1) }

func (s *Server) rateLoop() {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	last := s.samples.Load()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-t.C:
			cur := s.samples.Load()
			s.samplesSec.Store(math.Float64bits(float64(cur - last)))
			last = cur
		}
	}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"status":"ok","vessels":%d}`, s.world.Len())
	})
	mux.HandleFunc("GET /api/stats", s.handleStats)
	mux.HandleFunc("GET /ws", s.handleWS)
	return mux
}

func (s *Server) takeSlot() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.slots >= s.cfg.MaxClients {
		return false
	}
	s.slots++
	return true
}

func (s *Server) releaseSlot() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.slots--
}

func (s *Server) register(c *client) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextID++
	c.id = s.nextID
	s.clients[c] = struct{}{}
}

func (s *Server) unregister(c *client) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.clients, c)
}

// WaitClients blocks until every connection has finished closing, or ctx ends.
func (s *Server) WaitClients(ctx context.Context) {
	t := time.NewTicker(20 * time.Millisecond)
	defer t.Stop()
	for {
		s.mu.Lock()
		n := s.slots
		s.mu.Unlock()
		if n == 0 {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	if !s.takeSlot() {
		http.Error(w, "too many clients", http.StatusServiceUnavailable)
		return
	}
	defer s.releaseSlot()
	slow := 0
	if s.cfg.AllowDebugParams { // demo-only knob, never honour it unless explicitly enabled
		if v, err := strconv.Atoi(r.URL.Query().Get("slow_ms")); err == nil {
			slow = min(2000, max(0, v))
		}
	}
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		OriginPatterns:  s.cfg.AllowedOrigins,
		CompressionMode: websocket.CompressionDisabled, // protobuf is already compact
	})
	if err != nil {
		return // Accept already wrote the HTTP error
	}
	conn.SetReadLimit(4096) // client messages are tiny, anything bigger is abuse
	c := newClient(conn, s)
	c.slow = time.Duration(slow) * time.Millisecond
	s.register(c)
	defer s.unregister(c)
	// The server's root context, not r.Context(): shutdown must be able to close hijacked connections.
	c.run(s.ctx)
}
