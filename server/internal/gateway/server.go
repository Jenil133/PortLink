package gateway

import (
	"fmt"
	"net/http"

	"github.com/coder/websocket"
)

type Config struct {
	Addr           string
	AllowedOrigins []string // host:port patterns passed to websocket.AcceptOptions.OriginPatterns
}

type Server struct {
	cfg   Config
	world *World
}

func NewServer(cfg Config, world *World) *Server { return &Server{cfg: cfg, world: world} }

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
	c := &client{conn: conn, srv: s}
	c.run(r.Context())
}
