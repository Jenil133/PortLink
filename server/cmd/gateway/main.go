// Command gateway keeps the latest state of every vessel in memory and streams
// each WebSocket client the vessels inside its viewport.
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

	nc, _, err := bus.Connect(config.Env("NATS_URL", nats.DefaultURL), "gateway")
	if err != nil {
		return fmt.Errorf("connect to nats: %w", err)
	}
	defer nc.Close()

	world := gateway.NewWorld()

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

	srv := gateway.NewServer(gateway.Config{Addr: addr, AllowedOrigins: origins}, world)
	httpServer := &http.Server{Addr: addr, Handler: srv.Handler()}

	serveErr := make(chan error, 1)
	go func() { serveErr <- httpServer.ListenAndServe() }()
	slog.Info("gateway listening", "addr", addr, "allowed_origins", origins)

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

func splitOrigins(s string) []string {
	var out []string
	for _, o := range strings.Split(s, ",") {
		if o = strings.TrimSpace(o); o != "" {
			out = append(out, o)
		}
	}
	return out
}
