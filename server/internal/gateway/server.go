package gateway

import (
	"fmt"
	"net/http"
	"time"

	"github.com/coder/websocket"

	"github.com/portlink/lens/server/internal/store"
)

type Config struct {
	Addr           string
	AllowedOrigins []string      // host:port patterns passed to websocket.AcceptOptions.OriginPatterns
	LiveTick       time.Duration // from LIVE_TICK_MS, default 100 ms, clamped to [20 ms, 1 s]
	HistoryHours   int           // from HISTORY_HOURS, default 6
}

type Server struct {
	cfg   Config
	world *World
	st    *store.Store
	live  *liveSource
}

func NewServer(cfg Config, world *World, st *store.Store) *Server {
	return &Server{cfg: cfg, world: world, st: st, live: &liveSource{world: world}}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"status":"ok","vessels":%d}`, s.world.Len())
	})
	mux.HandleFunc("GET /ws", s.handleWS)
	return mux
}

func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		OriginPatterns:  s.cfg.AllowedOrigins,
		CompressionMode: websocket.CompressionDisabled, // protobuf is already compact
	})
	if err != nil {
		return // Accept already wrote the HTTP error
	}
	conn.SetReadLimit(4096) // client messages are tiny, anything bigger is abuse
	newClient(conn, s).run(r.Context())
}
