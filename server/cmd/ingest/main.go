// Command ingest moves samples from JetStream into TimescaleDB, acknowledging
// a batch only after its rows are stored.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"google.golang.org/protobuf/proto"

	"github.com/portlink/lens/server/internal/bus"
	"github.com/portlink/lens/server/internal/config"
	lensv1 "github.com/portlink/lens/server/internal/gen/lens/v1"
	"github.com/portlink/lens/server/internal/store"
)

const logEvery = 5 * time.Second

func main() {
	if err := run(); err != nil {
		slog.Error("ingest failed", "err", err)
		os.Exit(1)
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	dbURL := config.Env("DATABASE_URL", "")
	if dbURL == "" {
		return errors.New("DATABASE_URL is required")
	}

	nc, js, err := bus.Connect(config.Env("NATS_URL", nats.DefaultURL), "ingest")
	if err != nil {
		return fmt.Errorf("connect to nats: %w", err)
	}
	defer nc.Close()
	if err := bus.EnsureStream(ctx, js); err != nil {
		return fmt.Errorf("ensure stream: %w", err)
	}

	st, err := store.Open(ctx, dbURL)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer st.Pool.Close()

	// Durable pull consumer: survives restarts and resumes where it left off.
	cons, err := js.CreateOrUpdateConsumer(ctx, bus.StreamName, jetstream.ConsumerConfig{
		Durable:       "ingest",
		AckPolicy:     jetstream.AckExplicitPolicy,
		AckWait:       30 * time.Second,
		MaxAckPending: 20000,
	})
	if err != nil {
		return fmt.Errorf("create consumer: %w", err)
	}

	slog.Info("ingest running")
	consume(ctx, cons, st)
	return nil
}

func consume(ctx context.Context, cons jetstream.Consumer, st *store.Store) {
	known := map[uint32]struct{}{} // vessels already upserted by this process
	written := 0
	lastLog := time.Now()

	for ctx.Err() == nil {
		if time.Since(lastLog) >= logEvery {
			slog.Info("written", "rows", written, "window", logEvery)
			written, lastLog = 0, time.Now()
		}

		batch, err := cons.Fetch(2000, jetstream.FetchMaxWait(500*time.Millisecond))
		if err != nil {
			slog.Warn("fetch", "err", err)
			time.Sleep(time.Second)
			continue
		}
		var msgs []jetstream.Msg
		var rows [][]any
		for m := range batch.Messages() {
			var s lensv1.VesselSample
			if err := proto.Unmarshal(m.Data(), &s); err != nil || !validSample(&s) {
				_ = m.Term() // poison message: never redeliver
				continue
			}
			if _, ok := known[s.Mmsi]; !ok {
				if err := st.UpsertVessel(ctx, int64(s.Mmsi), s.Name, int32(s.VesselType)); err == nil {
					known[s.Mmsi] = struct{}{}
				}
			}
			rows = append(rows, []any{
				time.UnixMilli(s.TsMs).UTC(), int64(s.Mmsi), s.Lat, s.Lon, s.SogKn, s.CogDeg, s.HeadingDeg,
				s.EngineRpm, s.FuelRateLph, s.EngineTempC, s.FuelLevelPct,
			})
			msgs = append(msgs, m)
		}
		// A failed fetch can finish instantly with no messages. Back off so a JetStream
		// outage does not turn this loop into a busy spin.
		if err := batch.Error(); err != nil {
			slog.Warn("fetch batch", "err", err)
			if len(rows) == 0 {
				time.Sleep(time.Second)
			}
		}
		if len(rows) == 0 {
			continue
		}
		// Ack only after the rows are safely in Postgres. On failure, Nak so JetStream redelivers.
		if err := st.CopySamples(ctx, rows); err != nil {
			for _, m := range msgs {
				_ = m.Nak()
			}
			if ctx.Err() != nil {
				return // shutting down: JetStream redelivers this batch on the next start
			}
			slog.Error("copy", "err", err, "rows", len(rows))
			time.Sleep(time.Second)
			continue
		}
		for _, m := range msgs {
			_ = m.Ack()
		}
		written += len(rows)
	}
}

// validSample rejects anything the vessel_samples constraints or the rest of
// the pipeline cannot accept.
func validSample(s *lensv1.VesselSample) bool {
	if s.Mmsi == 0 || math.IsNaN(s.Lat) || math.IsNaN(s.Lon) {
		return false
	}
	if s.Lat < -90 || s.Lat > 90 || s.Lon < -180 || s.Lon > 180 {
		return false
	}
	age := time.Since(time.UnixMilli(s.TsMs))
	return age > -24*time.Hour && age < 24*time.Hour
}
