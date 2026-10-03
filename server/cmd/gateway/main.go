// Command gateway keeps the latest state of every vessel in memory and streams
// each WebSocket client only what its viewport needs, live or from history.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/nats-io/nats.go"
	"google.golang.org/protobuf/proto"

	"github.com/portlink/lens/server/internal/bus"
	"github.com/portlink/lens/server/internal/config"
	"github.com/portlink/lens/server/internal/gateway"
	lensv1 "github.com/portlink/lens/server/internal/gen/lens/v1"
	"github.com/portlink/lens/server/internal/model"
	"github.com/portlink/lens/server/internal/store"
)

const (
	evictEvery = 10 * time.Second
	evictAfter = 120 * time.Second
)

func main() {
	if err := run(); err != nil {
		slog.Error("gateway failed", "err", err)
		os.Exit(1)
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	addr := config.Env("GATEWAY_ADDR", ":8080")
	origins := splitOrigins(config.Env("ALLOWED_ORIGINS", ""))
	liveTickMs := min(1000, max(20, config.EnvInt("LIVE_TICK_MS", 100)))
	historyHours := config.EnvInt("HISTORY_HOURS", 6)
	if historyHours <= 0 {
		return fmt.Errorf("HISTORY_HOURS must be positive, got %d", historyHours)
	}
	dbURL := config.Env("DATABASE_URL", "")
	if dbURL == "" {
		return errors.New("DATABASE_URL is required")
	}

	st, err := store.Open(ctx, dbURL)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer st.Pool.Close()

	nc, _, err := bus.Connect(config.Env("NATS_URL", nats.DefaultURL), "gateway")
	if err != nil {
		return fmt.Errorf("connect to nats: %w", err)
	}
	defer nc.Close()

	world := gateway.NewWorld()

	// Bootstrap so the map is not empty until the first samples arrive. Failing here only costs
	// up to one second of an empty map, so it is a warning.
	states, err := st.LatestStates(ctx)
	if err != nil {
		slog.Warn("bootstrap from database failed", "err", err)
	}
	for _, s := range states {
		world.Upsert(s)
	}
	slog.Info("bootstrapped from database", "vessels", len(states))

	// Core subscription. The callback runs on a single goroutine per subscription, in order.
	sub, err := nc.Subscribe(bus.SubjectSamples+".>", func(m *nats.Msg) {
		var s lensv1.VesselSample
		if err := proto.Unmarshal(m.Data, &s); err != nil {
			return
		}
		world.Upsert(model.VesselState{
			MMSI: s.Mmsi, TsMs: s.TsMs, Lat: s.Lat, Lon: s.Lon,
			Sog: s.SogKn, Cog: s.CogDeg, Heading: s.HeadingDeg, Name: s.Name, VesselType: s.VesselType,
			EngineRPM: s.EngineRpm, FuelRateLph: s.FuelRateLph, EngineTempC: s.EngineTempC, FuelLevelPct: s.FuelLevelPct,
		})
	})
	if err != nil {
		return fmt.Errorf("subscribe: %w", err)
	}
	// Large pending limits: if the gateway stalls briefly we prefer buffering over NATS dropping us.
	_ = sub.SetPendingLimits(65536, 64<<20)

	go evictLoop(ctx, world)

	srv := gateway.NewServer(gateway.Config{
		Addr:           addr,
		AllowedOrigins: origins,
		LiveTick:       time.Duration(liveTickMs) * time.Millisecond,
		HistoryHours:   historyHours,
	}, world, st)
	httpServer := &http.Server{Addr: addr, Handler: srv.Handler()}

	serveErr := make(chan error, 1)
	go func() { serveErr <- httpServer.ListenAndServe() }()
	slog.Info("gateway listening", "addr", addr, "allowed_origins", origins, "live_tick_ms", liveTickMs, "history_hours", historyHours)

	select {
	case err := <-serveErr:
		return fmt.Errorf("serve: %w", err)
	case <-ctx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("shutdown: %w", err)
	}
	return nil
}

// evictLoop removes vessels that stopped reporting, so they leave every client's map.
func evictLoop(ctx context.Context, world *gateway.World) {
	t := time.NewTicker(evictEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			if n := world.EvictStale(now.Add(-evictAfter).UnixMilli()); n > 0 {
				slog.Info("evicted stale vessels", "count", n, "remaining", world.Len())
			}
		}
	}
}

func splitOrigins(s string) []string {
	var out []string
	for _, o := range strings.Split(s, ",") {
		if o = strings.TrimSpace(o); o != "" {
			out = append(out, o)
		}
	}
	return out
}
