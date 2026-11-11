package bus

import (
	"context"
	"fmt"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

const (
	StreamName      = "LENS"
	SubjectSamples  = "lens.samples"  // live samples:      lens.samples.<mmsi>
	SubjectBackfill = "lens.backfill" // historical fill:   lens.backfill.<mmsi>
)

func SampleSubject(mmsi uint32) string   { return fmt.Sprintf("%s.%d", SubjectSamples, mmsi) }
func BackfillSubject(mmsi uint32) string { return fmt.Sprintf("%s.%d", SubjectBackfill, mmsi) }

// connectWait bounds the first connection. NATS only listens once it has restored its
// JetStream streams, which takes seconds after a start with a large stream.
const connectWait = 30 * time.Second

// Connect retries forever on disconnect so a NATS restart does not kill the service, and retries
// the first connection for up to connectWait so a service can start together with NATS.
func Connect(url, name string) (*nats.Conn, jetstream.JetStream, error) {
	var nc *nats.Conn
	var err error
	for deadline := time.Now().Add(connectWait); ; time.Sleep(time.Second) {
		nc, err = nats.Connect(url,
			nats.Name(name),
			nats.MaxReconnects(-1),
			nats.ReconnectWait(time.Second),
		)
		if err == nil || time.Now().After(deadline) {
			break
		}
	}
	if err != nil {
		return nil, nil, err
	}
	js, err := jetstream.New(nc, jetstream.WithPublishAsyncMaxPending(8192))
	if err != nil {
		nc.Close()
		return nil, nil, err
	}
	return nc, js, nil
}

// EnsureStream is idempotent. Both feeder and ingest call it, so start order does not matter.
// Backfill uses a separate subject so the gateway (which only listens to lens.samples.>)
// never mistakes old data for live data.
func EnsureStream(ctx context.Context, js jetstream.JetStream) error {
	_, err := js.CreateOrUpdateStream(ctx, jetstream.StreamConfig{
		Name:      StreamName,
		Subjects:  []string{SubjectSamples + ".>", SubjectBackfill + ".>"},
		Storage:   jetstream.FileStorage,
		Retention: jetstream.LimitsPolicy,
		Discard:   jetstream.DiscardOld,
		MaxAge:    time.Hour,
		MaxBytes:  2 << 30, // 2 GiB
	})
	return err
}
