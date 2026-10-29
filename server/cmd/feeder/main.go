// Command feeder publishes vessel samples to NATS: live by default, or one
// pass over past time with -backfill.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"math"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"google.golang.org/protobuf/proto"

	"github.com/portlink/lens/server/internal/bus"
	"github.com/portlink/lens/server/internal/config"
	"github.com/portlink/lens/server/internal/feed"
	"github.com/portlink/lens/server/internal/model"
)

const logEvery = 5 * time.Second

func main() {
	if err := run(); err != nil {
		slog.Error("feeder failed", "err", err)
		os.Exit(1)
	}
}

func run() error {
	backfill := flag.Duration("backfill", 0, "publish this much past data, then exit")
	step := flag.Duration("step", 10*time.Second, "time between samples in backfill mode")
	flag.Parse()

	hz := config.EnvInt("FEED_HZ", 1)
	switch hz {
	case 1, 2, 5, 10:
	default:
		return fmt.Errorf("FEED_HZ must be 1, 2, 5 or 10, got %d", hz)
	}
	if *step <= 0 {
		return fmt.Errorf("-step must be positive, got %s", *step)
	}

	// Before the signal handler: loading an AIS file takes a while and SIGTERM should still end it.
	src, err := loadSource()
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	nc, js, err := bus.Connect(config.Env("NATS_URL", nats.DefaultURL), "feeder")
	if err != nil {
		return fmt.Errorf("connect to nats: %w", err)
	}
	defer nc.Close()
	if err := bus.EnsureStream(ctx, js); err != nil {
		return fmt.Errorf("ensure stream: %w", err)
	}

	if *backfill > 0 {
		return runBackfill(ctx, js, src, *backfill, *step)
	}
	slog.Info("feeder live", "vessels", src.Len(), "hz", hz)
	runLive(ctx, nc, src, hz)
	return nil
}

func loadSource() (feed.Source, error) {
	switch source := config.Env("FEED_SOURCE", "synthetic"); source {
	case "synthetic":
		vessels := config.EnvInt("FEED_VESSELS", 2000)
		if vessels <= 0 {
			return nil, fmt.Errorf("FEED_VESSELS must be positive, got %d", vessels)
		}
		return feed.NewSynthFleet(vessels, int64(config.EnvInt("FEED_SEED", 42))), nil
	case "ais":
		box, err := parseBBox(config.Env("AIS_BBOX", "-126,32,-116,49"))
		if err != nil {
			return nil, err
		}
		return feed.LoadAIS(config.Env("AIS_CSV_PATH", "../data/AIS_2024_07_01.csv"),
			box, config.EnvFloat("REPLAY_SPEED", 1), config.EnvInt("FANOUT", 1))
	default:
		return nil, fmt.Errorf("FEED_SOURCE must be synthetic or ais, got %q", source)
	}
}

// parseBBox reads "minLon,minLat,maxLon,maxLat".
func parseBBox(s string) (model.BBox, error) {
	parts := strings.Split(s, ",")
	if len(parts) != 4 {
		return model.BBox{}, fmt.Errorf("AIS_BBOX must be minLon,minLat,maxLon,maxLat, got %q", s)
	}
	var v [4]float64
	for i, p := range parts {
		f, err := strconv.ParseFloat(strings.TrimSpace(p), 64)
		if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
			return model.BBox{}, fmt.Errorf("AIS_BBOX must be minLon,minLat,maxLon,maxLat, got %q", s)
		}
		v[i] = f
	}
	b := model.BBox{MinLon: v[0], MinLat: v[1], MaxLon: v[2], MaxLat: v[3]}
	if b.MinLon >= b.MaxLon || b.MinLat >= b.MaxLat || b.MinLon < -180 || b.MaxLon > 180 || b.MinLat < -90 || b.MaxLat > 90 {
		return model.BBox{}, fmt.Errorf("AIS_BBOX is not a valid box: %q", s)
	}
	return b, nil
}

// Live loop. A 100 ms ticker with "slots" spreads the fleet evenly across each second
// instead of publishing 2,000 messages in one burst.
func runLive(ctx context.Context, nc *nats.Conn, src feed.Source, hz int) {
	slots := 10 / hz // hz=1 -> each vessel publishes on 1 of 10 ticks
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	report := time.NewTicker(logEvery)
	defer report.Stop()
	tick, published, failed := 0, 0, 0
	var lastErr error
	for {
		select {
		case <-ctx.Done():
			return
		case <-report.C:
			slog.Info("published", "samples", published, "window", logEvery)
			if failed > 0 {
				slog.Warn("publish failed", "samples", failed, "window", logEvery, "err", lastErr)
			}
			published, failed = 0, 0
		case now := <-ticker.C:
			for i := tick % slots; i < src.Len(); i += slots {
				s, ok := src.SampleAt(i, now)
				if !ok {
					continue
				}
				feed.FillTelemetry(s)
				data, err := proto.Marshal(s)
				if err != nil {
					slog.Error("marshal", "err", err)
					continue
				}
				// Core publish: fire and forget. The stream still stores it.
				if err := nc.Publish(bus.SampleSubject(s.Mmsi), data); err != nil {
					failed++
					lastErr = err
					continue
				}
				published++
			}
			tick++
		}
	}
}

// Backfill publishes past samples on lens.backfill.<mmsi> with async acks,
// pausing every 4,000 messages so we never outrun JetStream.
func runBackfill(ctx context.Context, js jetstream.JetStream, src feed.Source, span, step time.Duration) error {
	started := time.Now()
	end := started.Truncate(step)
	pending, total := 0, 0
	for t := end.Add(-span); t.Before(end); t = t.Add(step) {
		for i := 0; i < src.Len(); i++ {
			s, ok := src.SampleAt(i, t)
			if !ok {
				continue
			}
			feed.FillTelemetry(s)
			data, err := proto.Marshal(s)
			if err != nil {
				return fmt.Errorf("marshal: %w", err)
			}
			if _, err := js.PublishAsync(bus.BackfillSubject(s.Mmsi), data); err != nil {
				return err
			}
			total++
			if pending++; pending >= 4000 {
				if err := waitForAcks(ctx, js); err != nil {
					return err
				}
				pending = 0
			}
		}
	}
	if err := waitForAcks(ctx, js); err != nil {
		return err
	}
	slog.Info("backfill complete", "samples", total, "span", span, "step", step, "took", time.Since(started).Round(time.Millisecond))
	return nil
}

func waitForAcks(ctx context.Context, js jetstream.JetStream) error {
	select {
	case <-js.PublishAsyncComplete():
		return nil
	case <-time.After(30 * time.Second):
		return errors.New("backfill: timed out waiting for publish acks")
	case <-ctx.Done():
		return ctx.Err()
	}
}
