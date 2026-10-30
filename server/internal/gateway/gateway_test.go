package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"google.golang.org/protobuf/proto"

	lensv1 "github.com/portlink/lens/server/internal/gen/lens/v1"
	"github.com/portlink/lens/server/internal/model"
	"github.com/portlink/lens/server/internal/store"
)

// fakeStore satisfies historyStore. Every test that does not set a function gets empty results.
type fakeStore struct {
	asOf      func(view string, from, to time.Time, b model.BBox) ([]model.VesselState, error)
	series    func(view string, mmsi int64, from, to time.Time) ([]store.SeriesRow, error)
	startErr  error
	asOfCalls atomic.Int64
}

func (f *fakeStore) AsOf(_ context.Context, view string, from, to time.Time, b model.BBox) ([]model.VesselState, error) {
	f.asOfCalls.Add(1)
	if f.asOf != nil {
		return f.asOf(view, from, to, b)
	}
	return nil, nil
}

func (f *fakeStore) SeriesRows(_ context.Context, view string, mmsi int64, from, to time.Time) ([]store.SeriesRow, error) {
	if f.series != nil {
		return f.series(view, mmsi, from, to)
	}
	return nil, nil
}

func (f *fakeStore) HistoryStart(_ context.Context, hours int) (time.Time, error) {
	if f.startErr != nil {
		return time.Time{}, f.startErr
	}
	return time.Now().Add(-time.Duration(hours) * time.Hour), nil
}

var (
	bayBox   = &lensv1.BBox{MinLon: -123, MinLat: 37, MaxLon: -122, MaxLat: 38}
	coastBox = &lensv1.BBox{MinLon: -130, MinLat: 30, MaxLon: -115, MaxLat: 50}
	emptyBox = &lensv1.BBox{MinLon: -100, MinLat: 40, MaxLon: -99, MaxLat: 41}
)

func testConfig() Config {
	return Config{LiveTick: 20 * time.Millisecond, HistoryHours: 6, ClusterZoom: 6, MaxClients: 10}
}

// fleetOf returns a world with n vessels in the Bay box.
func fleetOf(n int) *World {
	w := NewWorld()
	for i := 1; i <= n; i++ {
		w.Upsert(model.VesselState{MMSI: uint32(i), TsMs: 1, Lat: 37.8, Lon: -122.9 + float64(i)*0.01, Name: "V"})
	}
	return w
}

type testServer struct {
	srv  *Server
	ts   *httptest.Server
	stop context.CancelFunc
}

func startServer(t *testing.T, cfg Config, world *World, st historyStore) *testServer {
	t.Helper()
	root, stop := context.WithCancel(context.Background())
	if st == nil {
		st = &fakeStore{}
	}
	srv := NewServer(root, cfg, world, st)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(func() {
		stop()
		ts.Close()
	})
	return &testServer{srv: srv, ts: ts, stop: stop}
}

func (s *testServer) wsURL(query string) string {
	return "ws" + strings.TrimPrefix(s.ts.URL, "http") + "/ws" + query
}

func (s *testServer) dial(t *testing.T, ctx context.Context, query string) *websocket.Conn {
	t.Helper()
	conn, _, err := websocket.Dial(ctx, s.wsURL(query), nil)
	if err != nil {
		t.Fatal(err)
	}
	conn.SetReadLimit(8 << 20)
	t.Cleanup(func() { conn.CloseNow() })
	return conn
}

func (s *testServer) stats(t *testing.T) StatsResponse {
	t.Helper()
	resp, err := http.Get(s.ts.URL + "/api/stats")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out StatsResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	return out
}

func readMsg(t *testing.T, ctx context.Context, conn *websocket.Conn) *lensv1.ServerMessage {
	t.Helper()
	_, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var m lensv1.ServerMessage
	if err := proto.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	return &m
}

func writeMsg(t *testing.T, ctx context.Context, conn *websocket.Conn, m *lensv1.ClientMessage) {
	t.Helper()
	data, err := proto.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.Write(ctx, websocket.MessageBinary, data); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func writeSub(t *testing.T, ctx context.Context, conn *websocket.Conn, sub *lensv1.Subscribe) {
	t.Helper()
	writeMsg(t, ctx, conn, &lensv1.ClientMessage{Msg: &lensv1.ClientMessage_Subscribe{Subscribe: sub}})
}

func nextFrameWithVessels(t *testing.T, ctx context.Context, conn *websocket.Conn) *lensv1.Frame {
	t.Helper()
	for {
		if f := readMsg(t, ctx, conn).GetFrame(); f != nil && len(f.Vessels) > 0 {
			return f
		}
	}
}

func nextFrame(t *testing.T, ctx context.Context, conn *websocket.Conn) *lensv1.Frame {
	t.Helper()
	for {
		if f := readMsg(t, ctx, conn).GetFrame(); f != nil {
			return f
		}
	}
}

// frameForView skips frames until the given view_id is echoed.
func frameForView(t *testing.T, ctx context.Context, conn *websocket.Conn, view uint32) *lensv1.Frame {
	t.Helper()
	for {
		if f := nextFrame(t, ctx, conn); f.ViewId == view {
			return f
		}
	}
}

// Flow under test: connect -> Hello -> Subscribe -> snapshot frame -> move one vessel -> delta with only that vessel.
func TestSnapshotThenDelta(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	world := NewWorld()
	for i := uint32(1); i <= 3; i++ {
		world.Upsert(model.VesselState{MMSI: i, TsMs: 1, Lat: 37.8, Lon: -122.5 + float64(i)*0.01, Name: "V"})
	}
	s := startServer(t, testConfig(), world, nil)
	conn := s.dial(t, ctx, "")

	if readMsg(t, ctx, conn).GetHello() == nil {
		t.Fatal("expected Hello first")
	}
	writeSub(t, ctx, conn, &lensv1.Subscribe{ViewId: 1, Zoom: 12, Bbox: bayBox})

	f := nextFrameWithVessels(t, ctx, conn)
	if !f.Snapshot || len(f.Vessels) != 3 {
		t.Fatalf("snapshot=%v vessels=%d", f.Snapshot, len(f.Vessels))
	}

	world.Upsert(model.VesselState{MMSI: 2, TsMs: 2, Lat: 37.81, Lon: -122.48, Name: "V"})
	f = nextFrameWithVessels(t, ctx, conn)
	if f.Snapshot || len(f.Vessels) != 1 || f.Vessels[0].Mmsi != 2 || f.Vessels[0].Name != nil {
		t.Fatalf("expected a position-only delta for vessel 2, got %v", f)
	}

	// Leaving the viewport must produce removals.
	writeSub(t, ctx, conn, &lensv1.Subscribe{ViewId: 2, Zoom: 12, Bbox: emptyBox})
	for {
		f := readMsg(t, ctx, conn).GetFrame()
		if f != nil && len(f.Removed) == 3 {
			break
		}
	}
}

// Drops must not corrupt state: the client ends up exactly on the vessel's final position.
func TestConflationKeepsStateExact(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cfg := testConfig()
	cfg.AllowDebugParams = true
	world := NewWorld()
	start := model.VesselState{MMSI: 1, TsMs: 1, Lat: 37.5, Lon: -122.5, Name: "V"}
	world.Upsert(start)
	s := startServer(t, cfg, world, nil)
	conn := s.dial(t, ctx, "?slow_ms=100")
	readMsg(t, ctx, conn)
	writeSub(t, ctx, conn, &lensv1.Subscribe{ViewId: 1, Zoom: 12, Bbox: bayBox})

	var mu sync.Mutex
	var latE6, lonE6 int32
	var dropped uint32
	go func() {
		for {
			_, data, err := conn.Read(ctx)
			if err != nil {
				return
			}
			var m lensv1.ServerMessage
			if proto.Unmarshal(data, &m) != nil {
				return
			}
			f := m.GetFrame()
			if f == nil {
				continue
			}
			mu.Lock()
			for _, d := range f.Vessels {
				if d.LatE6 != nil {
					latE6, lonE6 = *d.LatE6, *d.LonE6
				}
			}
			dropped = f.DroppedTotal
			mu.Unlock()
		}
	}()

	// Every step is at least the position threshold, so the last one is always worth sending.
	step := float64(posThresholdE6(12)) / 1e6
	final := start
	for i := int64(2); time.Duration(i)*5*time.Millisecond < time.Second; i++ {
		final.TsMs, final.Lat = i, final.Lat+step
		world.Upsert(final)
		time.Sleep(5 * time.Millisecond)
	}
	want := quantize(&final)

	deadline := time.Now().Add(10 * time.Second)
	for {
		mu.Lock()
		gotLat, gotLon, gotDropped := latE6, lonE6, dropped
		mu.Unlock()
		if gotLat == want.latE6 && gotLon == want.lonE6 && gotDropped > 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("position %d,%d want %d,%d, dropped %d", gotLat, gotLon, want.latE6, want.lonE6, gotDropped)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// Socket buffers absorb writes long after a reader has stopped. Frames must not pile up in them:
// a reader that comes back gets a short tail and then the current state, with the drops reported.
func TestStalledReaderFindsNoBacklog(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	world := NewWorld()
	v := model.VesselState{MMSI: 1, TsMs: 1, Lat: 37.5, Lon: -122.5, Name: "V"}
	world.Upsert(v)
	s := startServer(t, testConfig(), world, nil)
	conn := s.dial(t, ctx, "")
	readMsg(t, ctx, conn)
	writeSub(t, ctx, conn, &lensv1.Subscribe{ViewId: 1, Zoom: 22, Bbox: bayBox})
	nextFrame(t, ctx, conn)

	// Moves on every tick of the 20 ms ticker, so every tick has something to send.
	mctx, mcancel := context.WithCancel(ctx)
	defer mcancel()
	go func() {
		t := time.NewTicker(10 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-mctx.Done():
				return
			case <-t.C:
				v.TsMs++
				v.Lat += 0.0001
				world.Upsert(v)
			}
		}
	}()

	const stall = 6 * time.Second
	time.Sleep(stall / 2)

	// Well into the stall the client is behind: a Subscribe must not push more frames into the socket.
	framesOut := func() uint64 { return s.stats(t).Clients[0].FramesOut }
	before := framesOut()
	for view := uint32(100); view < 105; view++ {
		writeSub(t, ctx, conn, &lensv1.Subscribe{ViewId: view, Zoom: 22, Bbox: bayBox})
		time.Sleep(50 * time.Millisecond)
	}
	if after := framesOut(); after != before {
		t.Fatalf("%d frames were written for Subscribes while the client was behind", after-before)
	}
	time.Sleep(stall / 2)

	// Without the probe 300 frames would be waiting here: 6 s at 50 frames per second. With it, at
	// most about 1.5 s of frames got out before the overdue ping stopped them.
	const allowed = 120
	var frames int
	for {
		f := nextFrame(t, ctx, conn)
		frames++
		if time.Since(time.UnixMilli(f.TsMs)) < 200*time.Millisecond && f.DroppedTotal > 0 {
			if frames > allowed {
				t.Fatalf("%d stale frames came out of the socket before the current state, at most %d allowed", frames, allowed)
			}
			if f.DroppedTotal < 30 {
				t.Fatalf("only %d ticks were counted as dropped during a 6 s stall", f.DroppedTotal)
			}
			return
		}
		if frames > 2*allowed {
			t.Fatalf("still reading stale frames after %d, dropped %d", frames, f.DroppedTotal)
		}
	}
}

// A client that stopped answering pings is closed after the probe timeout, and only then.
func TestSilentClientIsClosed(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	s := startServer(t, testConfig(), fleetOf(2), nil)
	s.srv.probeTimeout = 1500 * time.Millisecond
	conn := s.dial(t, ctx, "")
	readMsg(t, ctx, conn) // and never reads again, so no ping gets an answer
	t0 := time.Now()
	waitUntil(t, 10*time.Second, "the silent client to be closed", func() bool { return len(s.stats(t).Clients) == 0 })
	if took := time.Since(t0); took < time.Second {
		t.Fatalf("closed after %s, before the probe timeout", took)
	}
}

func TestHealthyClientHasNoDrops(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cfg := testConfig()
	cfg.LiveTick = 100 * time.Millisecond
	s := startServer(t, cfg, fleetOf(50), nil)
	conn := s.dial(t, ctx, "")
	readMsg(t, ctx, conn)
	writeSub(t, ctx, conn, &lensv1.Subscribe{ViewId: 1, Zoom: 12, Bbox: bayBox})
	deadline := time.Now().Add(2500 * time.Millisecond) // long enough for two rounds of the probe
	var last *lensv1.Frame
	for time.Now().Before(deadline) {
		last = nextFrame(t, ctx, conn)
	}
	if last.DroppedTotal != 0 {
		t.Fatalf("dropped %d", last.DroppedTotal)
	}
}

func TestBehind(t *testing.T) {
	c := &client{born: time.Now().Add(-time.Hour)}
	now := c.elapsedMs()
	for _, tc := range []struct {
		name   string
		at     int64
		minRTT int64
		want   bool
	}{
		{"no ping in flight", 0, 0, false},
		{"the first ping has no measurement to compare with", now - 10*maxUnconfirmedMs, -1, false},
		{"just sent", now, 0, false},
		{"inside the allowance", now - maxUnconfirmedMs/2, 0, false},
		{"overdue", now - 2*maxUnconfirmedMs, 0, true},
		{"a distant link's own round trip does not count", now - 2*maxUnconfirmedMs, maxUnconfirmedMs + 200, false},
		{"overdue on a distant link", now - 4*maxUnconfirmedMs, maxUnconfirmedMs + 200, true},
	} {
		c.probeAtMs.Store(tc.at)
		c.probeMinMs.Store(tc.minRTT)
		if got := c.behind(); got != tc.want {
			t.Errorf("%s: behind=%v", tc.name, got)
		}
	}
}

// delayProxy returns a URL that reaches the server through a TCP proxy adding a delay in each direction.
func delayProxy(t *testing.T, s *testServer, oneWay time.Duration) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	target := strings.TrimPrefix(s.ts.URL, "http://")
	type chunk struct {
		data []byte
		due  time.Time
	}
	pump := func(src, dst net.Conn) {
		q := make(chan chunk, 4096)
		go func() {
			defer dst.Close()
			for c := range q {
				time.Sleep(time.Until(c.due))
				if _, err := dst.Write(c.data); err != nil {
					src.Close()
					for range q {
					}
					return
				}
			}
		}()
		defer close(q)
		buf := make([]byte, 4096)
		for {
			n, err := src.Read(buf)
			if n > 0 {
				q <- chunk{append([]byte(nil), buf[:n]...), time.Now().Add(oneWay)}
			}
			if err != nil {
				return
			}
		}
	}
	go func() {
		for {
			client, err := ln.Accept()
			if err != nil {
				return
			}
			server, err := net.Dial("tcp", target)
			if err != nil {
				client.Close()
				return
			}
			go pump(client, server)
			go pump(server, client)
		}
	}()
	return "ws://" + ln.Addr().String() + "/ws"
}

// Distance is not backpressure: far beyond the 500 ms allowance, a client that keeps up gets every
// frame and no false drops, and its heartbeat stays on time.
func TestDistantClientIsNotBehind(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cfg := testConfig()
	cfg.LiveTick = 100 * time.Millisecond
	world := NewWorld()
	v := model.VesselState{MMSI: 1, TsMs: 1, Lat: 37.5, Lon: -122.5, Name: "V"}
	world.Upsert(v)
	s := startServer(t, cfg, world, nil)
	conn, _, err := websocket.Dial(ctx, delayProxy(t, s, 500*time.Millisecond), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()
	readMsg(t, ctx, conn)
	writeSub(t, ctx, conn, &lensv1.Subscribe{ViewId: 1, Zoom: 22, Bbox: bayBox})
	mctx, mcancel := context.WithCancel(ctx)
	defer mcancel()
	go func() {
		t := time.NewTicker(25 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-mctx.Done():
				return
			case <-t.C:
				v.TsMs++
				v.Lat += 0.0001
				world.Upsert(v)
			}
		}
	}()
	nextFrame(t, ctx, conn)
	t0 := time.Now()
	frames := 0
	var last *lensv1.Frame
	for time.Since(t0) < 5*time.Second {
		last = nextFrame(t, ctx, conn)
		frames++
	}
	if last.DroppedTotal != 0 || frames < 15 {
		t.Fatalf("%d frames in 5 s at a 1 s round trip, %d dropped", frames, last.DroppedTotal)
	}
}

// The frontend times seeks and marks the firehose switch from the first frame echoing a view_id,
// so a changed view_id is answered at once even when nothing else changed.
func TestChangedViewIDIsAnsweredAtOnce(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	s := startServer(t, testConfig(), fleetOf(3), nil)
	conn := s.dial(t, ctx, "")
	readMsg(t, ctx, conn)
	writeSub(t, ctx, conn, &lensv1.Subscribe{ViewId: 1, Zoom: 12, Bbox: bayBox})
	nextFrameWithVessels(t, ctx, conn)

	for view := uint32(2); view < 6; view++ {
		time.Sleep(150 * time.Millisecond) // well away from the next heartbeat, a second after the last frame
		t0 := time.Now()
		writeSub(t, ctx, conn, &lensv1.Subscribe{ViewId: view, Zoom: 12, Bbox: bayBox})
		f := frameForView(t, ctx, conn, view)
		if took := time.Since(t0); took > 500*time.Millisecond {
			t.Fatalf("view %d took %s, that is the heartbeat, not an answer", view, took)
		}
		if len(f.Vessels) != 0 || len(f.Removed) != 0 || f.Snapshot {
			t.Fatalf("nothing changed, the frame must be empty: %v", f)
		}
	}

	// The same view_id again is not a change: it waits for the heartbeat like any idle tick.
	nextFrame(t, ctx, conn)
	t0 := time.Now()
	writeSub(t, ctx, conn, &lensv1.Subscribe{ViewId: 5, Zoom: 12, Bbox: bayBox})
	nextFrame(t, ctx, conn)
	if took := time.Since(t0); took < 500*time.Millisecond {
		t.Fatalf("a re-sent view_id was answered after %s", took)
	}
}

func TestHeartbeatKeepsAnEmptyViewAlive(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	s := startServer(t, testConfig(), fleetOf(3), nil)
	conn := s.dial(t, ctx, "")
	readMsg(t, ctx, conn)
	writeSub(t, ctx, conn, &lensv1.Subscribe{ViewId: 1, Zoom: 12, Bbox: emptyBox})
	first := nextFrame(t, ctx, conn)
	if !first.Snapshot || first.InView != 0 {
		t.Fatalf("first frame: %v", first)
	}
	t0 := time.Now()
	for i := uint32(1); i <= 3; i++ {
		f := nextFrame(t, ctx, conn)
		if f.Seq != first.Seq+i || f.Snapshot || len(f.Vessels) != 0 {
			t.Fatalf("heartbeat %d: %v", i, f)
		}
	}
	if took := time.Since(t0); took < 2500*time.Millisecond || took > 5*time.Second {
		t.Fatalf("3 heartbeats took %s", took)
	}
}

// Ten vessels near San Francisco and five near Seattle.
func twoHarbours() *World {
	w := NewWorld()
	for i := 1; i <= 15; i++ {
		lat, lon := 37.8, -122.5
		if i > 10 {
			lat, lon = 47.6, -122.4
		}
		w.Upsert(model.VesselState{MMSI: uint32(i), TsMs: 1, Lat: lat + float64(i)*0.001, Lon: lon, Name: "V"})
	}
	return w
}

func TestClusteredFrames(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	s := startServer(t, testConfig(), twoHarbours(), nil)
	conn := s.dial(t, ctx, "")
	readMsg(t, ctx, conn)
	writeSub(t, ctx, conn, &lensv1.Subscribe{ViewId: 1, Zoom: 5, Bbox: coastBox})

	check := func(f *lensv1.Frame) {
		t.Helper()
		if !f.Clustered || len(f.Vessels) != 0 || len(f.Removed) != 0 {
			t.Fatalf("clustered frames hold clusters only: %v", f)
		}
		var sum uint32
		for _, c := range f.Clusters {
			sum += c.Count
		}
		if sum != 15 || f.InView != 15 || len(f.Clusters) >= 15 {
			t.Fatalf("in_view is the number of vessels, not clusters: in_view=%d sum=%d clusters=%d", f.InView, sum, len(f.Clusters))
		}
	}
	f := nextFrame(t, ctx, conn)
	if !f.Snapshot {
		t.Fatal("first frame must be a snapshot")
	}
	check(f)

	// At most one per second, each with the complete list.
	t0 := time.Now()
	for i := 0; i < 3; i++ {
		f = nextFrame(t, ctx, conn)
		if f.Snapshot {
			t.Fatal("only the first clustered frame is a snapshot")
		}
		check(f)
	}
	if took := time.Since(t0); took < 2700*time.Millisecond {
		t.Fatalf("3 frames in %s is more than one per second", took)
	}

	// A burst of view changes does not raise the rate. The latest view is echoed by the next frame.
	for view := uint32(2); view < 7; view++ {
		writeSub(t, ctx, conn, &lensv1.Subscribe{ViewId: view, Zoom: 5, Bbox: coastBox})
		time.Sleep(50 * time.Millisecond)
	}
	f = nextFrame(t, ctx, conn)
	if f.ViewId != 6 {
		t.Fatalf("view_id %d, want the latest, 6", f.ViewId)
	}
	check(f)

	// Crossing the threshold is a render change, answered at once with a snapshot, both ways.
	t0 = time.Now()
	writeSub(t, ctx, conn, &lensv1.Subscribe{ViewId: 7, Zoom: 12, Bbox: coastBox})
	f = frameForView(t, ctx, conn, 7)
	if !f.Snapshot || f.Clustered || len(f.Vessels) != 15 || f.InView != 15 {
		t.Fatalf("zooming in: %v", f)
	}
	writeSub(t, ctx, conn, &lensv1.Subscribe{ViewId: 8, Zoom: 5, Bbox: coastBox})
	f = frameForView(t, ctx, conn, 8)
	if !f.Snapshot {
		t.Fatalf("zooming out: %v", f)
	}
	check(f)
	if took := time.Since(t0); took > time.Second {
		t.Fatalf("two render changes took %s, they must not wait for the once-per-second rule", took)
	}
}

func TestClusteredFramesInHistory(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	st := &fakeStore{asOf: func(view string, _, _ time.Time, _ model.BBox) ([]model.VesselState, error) {
		if view != "samples_10m" {
			t.Errorf("zoom 5 must read samples_10m, read %s", view)
		}
		return []model.VesselState{{MMSI: 1, Lat: 37.8, Lon: -122.5}, {MMSI: 2, Lat: 37.81, Lon: -122.5}}, nil
	}}
	s := startServer(t, testConfig(), NewWorld(), st)
	conn := s.dial(t, ctx, "")
	readMsg(t, ctx, conn)
	writeSub(t, ctx, conn, &lensv1.Subscribe{ViewId: 1, Zoom: 5, Bbox: coastBox, Mode: lensv1.Mode_MODE_HISTORY, CursorMs: time.Now().Add(-time.Hour).UnixMilli()})
	f := nextFrame(t, ctx, conn)
	if !f.Clustered || !f.Snapshot || f.Tier != lensv1.Tier_TIER_10M || f.InView != 2 || len(f.Clusters) != 1 || f.Clusters[0].Count != 2 {
		t.Fatalf("got %v", f)
	}
}

func TestClusterZoomIsConfigurable(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cfg := testConfig()
	cfg.ClusterZoom = 0
	s := startServer(t, cfg, twoHarbours(), nil)
	conn := s.dial(t, ctx, "")
	readMsg(t, ctx, conn)
	writeSub(t, ctx, conn, &lensv1.Subscribe{ViewId: 1, Zoom: 3, Bbox: coastBox})
	if f := nextFrame(t, ctx, conn); f.Clustered || len(f.Vessels) != 15 {
		t.Fatalf("CLUSTER_ZOOM=0 means no clustering: %v", f)
	}
}

func TestFirehose(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	world := twoHarbours()
	s := startServer(t, testConfig(), world, nil)
	conn := s.dial(t, ctx, "")
	readMsg(t, ctx, conn)

	// Box and zoom are ignored: Seattle is outside this box, and zoom 3 would otherwise cluster.
	writeSub(t, ctx, conn, &lensv1.Subscribe{ViewId: 1, Zoom: 3, Bbox: bayBox, Firehose: true})
	f := nextFrame(t, ctx, conn)
	if !f.Snapshot || f.Clustered || len(f.Vessels) != 15 || f.InView != 15 {
		t.Fatalf("first firehose frame: snapshot=%v clustered=%v vessels=%d in_view=%d", f.Snapshot, f.Clustered, len(f.Vessels), f.InView)
	}
	full := func(d *lensv1.VesselDelta) bool {
		return d.LatE6 != nil && d.LonE6 != nil && d.SogDkn != nil && d.CogDdeg != nil && d.HeadingDeg != nil && d.Name != nil &&
			d.VesselType != nil && d.EngineRpm != nil && d.FuelRateDlph != nil && d.EngineTempDc != nil && d.FuelLevelDpct != nil
	}
	for _, d := range f.Vessels {
		if !full(d) {
			t.Fatalf("firehose must send every field: %v", d)
		}
	}

	// A new sample resends that vessel in full, even if it did not move at all.
	world.Upsert(model.VesselState{MMSI: 4, TsMs: 2, Lat: 37.804, Lon: -122.5, Name: "V"})
	f = nextFrameWithVessels(t, ctx, conn)
	if f.Snapshot || len(f.Vessels) != 1 || f.Vessels[0].Mmsi != 4 || !full(f.Vessels[0]) || f.InView != 15 {
		t.Fatalf("new sample: %v", f)
	}

	// No new sample, nothing but heartbeats.
	for i := 0; i < 2; i++ {
		if f := nextFrame(t, ctx, conn); len(f.Vessels) != 0 {
			t.Fatalf("the same sample must not be resent: %v", f)
		}
	}

	// Leaving firehose is a render change: snapshot, only what is in the box, no telemetry.
	writeSub(t, ctx, conn, &lensv1.Subscribe{ViewId: 2, Zoom: 12, Bbox: bayBox})
	f = frameForView(t, ctx, conn, 2)
	if !f.Snapshot || len(f.Vessels) != 10 || f.InView != 10 {
		t.Fatalf("back to lens: snapshot=%v vessels=%d in_view=%d", f.Snapshot, len(f.Vessels), f.InView)
	}
	for _, d := range f.Vessels {
		if d.EngineRpm != nil {
			t.Fatalf("lens mode sends telemetry for the selected vessel only: %v", d)
		}
	}
}

func TestFirehoseIsForcedOffInHistory(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	st := &fakeStore{asOf: func(string, time.Time, time.Time, model.BBox) ([]model.VesselState, error) {
		return []model.VesselState{{MMSI: 1, Lat: 37.8, Lon: -122.5}}, nil
	}}
	s := startServer(t, testConfig(), twoHarbours(), st)
	conn := s.dial(t, ctx, "")
	readMsg(t, ctx, conn)
	writeSub(t, ctx, conn, &lensv1.Subscribe{ViewId: 1, Zoom: 12, Bbox: bayBox, Firehose: true, Mode: lensv1.Mode_MODE_HISTORY, CursorMs: time.Now().Add(-time.Hour).UnixMilli()})
	f := nextFrame(t, ctx, conn)
	if f.Tier != lensv1.Tier_TIER_1S || len(f.Vessels) != 1 || f.Vessels[0].EngineRpm != nil {
		t.Fatalf("got %v", f)
	}
	if st := s.stats(t); len(st.Clients) != 1 || st.Clients[0].Render != "lens" || st.Clients[0].Mode != "history" {
		t.Fatalf("stats: %+v", st.Clients)
	}
}

func TestTelemetryOnlyForTheSelectedVessel(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	s := startServer(t, testConfig(), fleetOf(3), nil)
	conn := s.dial(t, ctx, "")
	readMsg(t, ctx, conn)
	writeSub(t, ctx, conn, &lensv1.Subscribe{ViewId: 1, Zoom: 12, Bbox: bayBox, SelectedMmsi: 2})
	f := nextFrameWithVessels(t, ctx, conn)
	if len(f.Vessels) != 3 {
		t.Fatalf("got %v", f)
	}
	for _, d := range f.Vessels {
		if (d.Mmsi == 2) != (d.EngineRpm != nil) {
			t.Fatalf("vessel %d, telemetry %v", d.Mmsi, d.EngineRpm)
		}
	}
}

func TestStatsEndpoint(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cfg := testConfig()
	cfg.AllowDebugParams = true
	s := startServer(t, cfg, fleetOf(5), nil)

	resp, err := http.Get(s.ts.URL + "/api/stats")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "application/json" || !strings.Contains(string(body), `"clients":[]`) {
		t.Fatalf("%d %s %s", resp.StatusCode, resp.Header.Get("Content-Type"), body)
	}

	for i := 0; i < 2500; i++ {
		s.srv.CountSample()
	}
	waitUntil(t, 3*time.Second, "the sample rate", func() bool { return s.stats(t).SamplesPerSec > 0 })

	conn := s.dial(t, ctx, "?slow_ms=7")
	readMsg(t, ctx, conn)
	waitUntil(t, 3*time.Second, "the new client to be listed", func() bool {
		c := s.stats(t).Clients
		return len(c) == 1 && c[0].Mode == "none"
	})
	for _, tc := range []struct {
		sub    *lensv1.Subscribe
		render string
		inView uint32
	}{
		{&lensv1.Subscribe{ViewId: 1, Zoom: 9.5, Bbox: bayBox}, "lens", 5},
		{&lensv1.Subscribe{ViewId: 2, Zoom: 5, Bbox: emptyBox}, "clustered", 0},
		{&lensv1.Subscribe{ViewId: 3, Zoom: 5, Bbox: emptyBox, Firehose: true}, "firehose", 5},
	} {
		writeSub(t, ctx, conn, tc.sub)
		f := frameForView(t, ctx, conn, tc.sub.ViewId)
		if f.InView != tc.inView {
			t.Fatalf("%s frame: in_view %d", tc.render, f.InView)
		}
		waitUntil(t, 3*time.Second, tc.render+" in the stats", func() bool {
			st := s.stats(t)
			if len(st.Clients) != 1 {
				return false
			}
			c := st.Clients[0]
			return c.Mode == "live" && c.Render == tc.render && c.Zoom == tc.sub.Zoom && c.InView == tc.inView &&
				c.FramesOut > 0 && c.BytesOut > 0 && c.SlowMs == 7 && c.ID == 1 && st.Vessels == 5 && st.SamplesTotal == 2500 && st.ServerTimeMs > 0
		})
	}
	// The JSON field names are part of the contract.
	resp, err = http.Get(s.ts.URL + "/api/stats")
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	for _, name := range []string{"server_time_ms", "vessels", "samples_total", "samples_per_sec", "clients", "id", "mode", "render", "zoom", "in_view", "bytes_out", "frames_out", "dropped", "slow_ms"} {
		if !strings.Contains(string(body), `"`+name+`":`) {
			t.Fatalf("no %s in %s", name, body)
		}
	}
}

func waitUntil(t *testing.T, limit time.Duration, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(limit)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestHealthz(t *testing.T) {
	s := startServer(t, testConfig(), fleetOf(4), nil)
	resp, err := http.Get(s.ts.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || string(body) != `{"status":"ok","vessels":4}` {
		t.Fatalf("%d %s", resp.StatusCode, body)
	}
}

func TestSlowParamNeedsTheDebugSwitch(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	for _, c := range []struct {
		allow bool
		query string
		want  int
	}{
		{false, "?slow_ms=300", 0},
		{true, "?slow_ms=300", 300},
		{true, "?slow_ms=99999", 2000},
		{true, "?slow_ms=-5", 0},
		{true, "?slow_ms=abc", 0},
		{true, "", 0},
	} {
		cfg := testConfig()
		cfg.AllowDebugParams = c.allow
		s := startServer(t, cfg, NewWorld(), nil)
		conn := s.dial(t, ctx, c.query)
		readMsg(t, ctx, conn)
		waitUntil(t, 3*time.Second, "the client to be listed", func() bool { return len(s.stats(t).Clients) == 1 })
		if got := s.stats(t).Clients[0].SlowMs; got != c.want {
			t.Errorf("allow=%v %q: slow_ms %d, want %d", c.allow, c.query, got, c.want)
		}
	}
}

func TestMaxClientsUnderABurst(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cfg := testConfig()
	cfg.MaxClients = 3
	s := startServer(t, cfg, NewWorld(), nil)

	const attempts = 40
	var mu sync.Mutex
	var open []*websocket.Conn
	refused := 0
	var wg sync.WaitGroup
	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			conn, resp, err := websocket.Dial(ctx, s.wsURL(""), nil)
			mu.Lock()
			defer mu.Unlock()
			if err == nil {
				open = append(open, conn)
				return
			}
			if resp == nil || resp.StatusCode != http.StatusServiceUnavailable {
				t.Errorf("want 503, got %v, %v", resp, err)
			}
			refused++
		}()
	}
	wg.Wait()
	if len(open) != 3 || refused != attempts-3 {
		t.Fatalf("accepted %d and refused %d with a limit of 3", len(open), refused)
	}

	// A closed connection gives its place back.
	open[0].CloseNow()
	waitUntil(t, 5*time.Second, "a free place", func() bool {
		conn, _, err := websocket.Dial(ctx, s.wsURL(""), nil)
		if err != nil {
			return false
		}
		open[0] = conn
		return true
	})
	for _, c := range open {
		c.CloseNow()
	}
}

func TestRefusedUpgradeGivesThePlaceBack(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cfg := testConfig()
	cfg.MaxClients = 1
	s := startServer(t, cfg, NewWorld(), nil)
	for i := 0; i < 5; i++ {
		_, resp, err := websocket.Dial(ctx, s.wsURL(""), &websocket.DialOptions{HTTPHeader: http.Header{"Origin": {"http://evil.example"}}})
		if err == nil || resp == nil || resp.StatusCode != http.StatusForbidden {
			t.Fatalf("a foreign origin must get 403: %v, %v", resp, err)
		}
	}
	conn := s.dial(t, ctx, "")
	readMsg(t, ctx, conn)
}

func TestOriginRules(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cfg := testConfig()
	cfg.AllowedOrigins = []string{"localhost:5173", "localhost:8088"}
	s := startServer(t, cfg, NewWorld(), nil)
	for origin, want := range map[string]int{
		"http://localhost:5173": http.StatusSwitchingProtocols,
		"http://localhost:8088": http.StatusSwitchingProtocols,
		"http://localhost:9999": http.StatusForbidden,
		"http://evil.example":   http.StatusForbidden,
	} {
		conn, resp, err := websocket.Dial(ctx, s.wsURL(""), &websocket.DialOptions{HTTPHeader: http.Header{"Origin": {origin}}})
		if resp == nil || resp.StatusCode != want {
			t.Errorf("%s: got %v, %v, want status %d", origin, resp, err, want)
		}
		if err == nil {
			conn.CloseNow()
		}
	}
}

func TestSubscribeRateLimit(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	s := startServer(t, testConfig(), fleetOf(2), nil)
	conn := s.dial(t, ctx, "")
	readMsg(t, ctx, conn)
	const burst = 200
	for view := uint32(1); view <= burst; view++ {
		writeSub(t, ctx, conn, &lensv1.Subscribe{ViewId: view, Zoom: 12, Bbox: bayBox})
	}
	// The burst can straddle a second, so one or two errors are both right. Hundreds are not.
	// No read deadline: a read that times out closes the socket, and heartbeats keep this loop moving.
	limited := 0
	var highest uint32
	for end := time.Now().Add(1100 * time.Millisecond); time.Now().Before(end); {
		m := readMsg(t, ctx, conn)
		if e := m.GetError(); e != nil {
			if e.Code != 4 || e.Message != "too many subscribes" {
				t.Fatalf("got %v", e)
			}
			limited++
		}
		if f := m.GetFrame(); f != nil {
			highest = max(highest, f.ViewId)
		}
	}
	if limited < 1 || limited > 2 || highest > 2*maxSubscribesPerSec {
		t.Fatalf("%d code 4 errors, highest view honoured %d", limited, highest)
	}
}

// After a limited second the connection works normally again.
func TestRateLimitResets(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	s := startServer(t, testConfig(), fleetOf(2), nil)
	conn := s.dial(t, ctx, "")
	readMsg(t, ctx, conn)
	for view := uint32(1); view <= 100; view++ {
		writeSub(t, ctx, conn, &lensv1.Subscribe{ViewId: view, Zoom: 12, Bbox: bayBox})
	}
	time.Sleep(2100 * time.Millisecond)
	writeSub(t, ctx, conn, &lensv1.Subscribe{ViewId: 1000, Zoom: 12, Bbox: bayBox})
	frameForView(t, ctx, conn, 1000)
}

func TestShutdownClosesWithGoingAway(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	s := startServer(t, testConfig(), fleetOf(5), nil)
	type outcome struct {
		code websocket.StatusCode
		err  error
	}
	const clients = 5
	results := make(chan outcome, clients)
	for i := 0; i < clients; i++ {
		conn := s.dial(t, ctx, "")
		readMsg(t, ctx, conn)
		writeSub(t, ctx, conn, &lensv1.Subscribe{ViewId: 1, Zoom: 12, Bbox: bayBox})
		nextFrame(t, ctx, conn)
		go func() {
			for {
				if _, _, err := conn.Read(ctx); err != nil {
					results <- outcome{websocket.CloseStatus(err), err}
					return
				}
			}
		}()
	}
	t0 := time.Now()
	s.stop()
	for i := 0; i < clients; i++ {
		if r := <-results; r.code != websocket.StatusGoingAway {
			t.Errorf("client %d: close code %d, want 1001: %v", i, r.code, r.err)
		}
	}
	wctx, wcancel := context.WithTimeout(ctx, 5*time.Second)
	defer wcancel()
	s.srv.WaitClients(wctx)
	if wctx.Err() != nil {
		t.Fatal("clients did not finish closing")
	}
	s.srv.mu.Lock()
	taken := s.srv.slots
	s.srv.mu.Unlock()
	if taken != 0 {
		t.Fatalf("WaitClients returned with %d connections still closing", taken)
	}
	if took := time.Since(t0); took > 3*time.Second {
		t.Fatalf("shutdown took %s", took)
	}
}

func TestBadInputKeepsTheSubscription(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	s := startServer(t, testConfig(), fleetOf(2), nil)
	conn := s.dial(t, ctx, "")
	readMsg(t, ctx, conn)
	writeSub(t, ctx, conn, &lensv1.Subscribe{ViewId: 1, Zoom: 12, Bbox: bayBox})
	nextFrame(t, ctx, conn)

	nextError := func() *lensv1.ServerError {
		t.Helper()
		for {
			m := readMsg(t, ctx, conn)
			if e := m.GetError(); e != nil {
				return e
			}
		}
	}
	if err := conn.Write(ctx, websocket.MessageBinary, []byte{0xff, 0xff, 0xff}); err != nil {
		t.Fatal(err)
	}
	if e := nextError(); e.Code != 1 {
		t.Fatalf("garbage: %v", e)
	}
	writeSub(t, ctx, conn, &lensv1.Subscribe{ViewId: 2, Zoom: 12})
	if e := nextError(); e.Code != 2 {
		t.Fatalf("no bbox: %v", e)
	}
	// The old subscription is still the one in force.
	if f := nextFrame(t, ctx, conn); f.ViewId != 1 {
		t.Fatalf("view_id %d, want 1", f.ViewId)
	}
	if err := conn.Write(ctx, websocket.MessageText, []byte("hello")); err != nil {
		t.Fatal(err)
	}
	writeMsg(t, ctx, conn, &lensv1.ClientMessage{Msg: &lensv1.ClientMessage_Ping{Ping: &lensv1.Ping{ClientTimeMs: 42}}})
	for {
		if p := readMsg(t, ctx, conn).GetPong(); p != nil {
			if p.ClientTimeMs != 42 || p.ServerTimeMs == 0 {
				t.Fatalf("pong: %v", p)
			}
			return
		}
	}
}

func TestHello(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	s := startServer(t, testConfig(), NewWorld(), nil)
	h := readMsg(t, ctx, s.dial(t, ctx, "")).GetHello()
	if h == nil || h.ProtocolVersion != 1 || h.LiveTickMs != 20 {
		t.Fatalf("got %v", h)
	}
	if age := time.Since(time.UnixMilli(h.ServerTimeMs)); age > 2*time.Second || age < -2*time.Second {
		t.Fatalf("server_time_ms is %s off", age)
	}
	if back := time.Since(time.UnixMilli(h.HistoryStartMs)); back < 5*time.Hour || back > 7*time.Hour {
		t.Fatalf("history starts %s ago", back)
	}

	// No history available: the scrubber gets a zero range and the connection still works.
	s2 := startServer(t, testConfig(), NewWorld(), &fakeStore{startErr: errors.New("database down")})
	h = readMsg(t, ctx, s2.dial(t, ctx, "")).GetHello()
	if h == nil || h.HistoryStartMs != h.ServerTimeMs {
		t.Fatalf("got %v", h)
	}
}

func TestHistorySeekAndFailure(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var failing atomic.Bool
	st := &fakeStore{}
	st.asOf = func(view string, from, to time.Time, b model.BBox) ([]model.VesselState, error) {
		if failing.Load() {
			return nil, errors.New("database down")
		}
		return []model.VesselState{{MMSI: 1, TsMs: to.UnixMilli(), Lat: 37.8, Lon: -122.5, Name: "OLD"}}, nil
	}
	s := startServer(t, testConfig(), fleetOf(3), st)
	conn := s.dial(t, ctx, "")
	readMsg(t, ctx, conn)
	cursor := time.Now().Add(-time.Hour).UnixMilli()

	for _, c := range []struct {
		zoom float32
		tier lensv1.Tier
	}{{6, lensv1.Tier_TIER_10M}, {8, lensv1.Tier_TIER_1M}, {12, lensv1.Tier_TIER_1S}} {
		view := uint32(c.zoom)
		writeSub(t, ctx, conn, &lensv1.Subscribe{ViewId: view, Zoom: c.zoom, Bbox: bayBox, Mode: lensv1.Mode_MODE_HISTORY, CursorMs: cursor})
		f := frameForView(t, ctx, conn, view)
		if f.Tier != c.tier || f.TsMs != cursor || f.InView != 1 {
			t.Fatalf("zoom %v: tier %v ts %d in_view %d", c.zoom, f.Tier, f.TsMs, f.InView)
		}
	}

	// Paused on the same bucket: no further queries.
	calls := st.asOfCalls.Load()
	time.Sleep(300 * time.Millisecond)
	if extra := st.asOfCalls.Load() - calls; extra != 0 {
		t.Fatalf("%d queries while paused", extra)
	}

	failing.Store(true)
	writeSub(t, ctx, conn, &lensv1.Subscribe{ViewId: 20, Zoom: 12, Bbox: bayBox, Mode: lensv1.Mode_MODE_HISTORY, CursorMs: cursor - 60_000})
	for {
		m := readMsg(t, ctx, conn)
		if e := m.GetError(); e != nil {
			if e.Code != 3 {
				t.Fatalf("got %v", e)
			}
			break
		}
	}
	// Live does not need the database, and back to live is not a render change.
	writeSub(t, ctx, conn, &lensv1.Subscribe{ViewId: 21, Zoom: 12, Bbox: bayBox})
	f := frameForView(t, ctx, conn, 21)
	if f.Tier != lensv1.Tier_TIER_LIVE || f.Snapshot || f.InView != 3 {
		t.Fatalf("back to live: %v", f)
	}
}

// One query and one error every 2 s per client, not one per tick, and live stays immediate.
func TestFailingDatabaseIsNotHammered(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	st := &fakeStore{asOf: func(string, time.Time, time.Time, model.BBox) ([]model.VesselState, error) {
		return nil, errors.New("database down")
	}}
	s := startServer(t, testConfig(), fleetOf(3), st)
	conn := s.dial(t, ctx, "")
	readMsg(t, ctx, conn)
	writeSub(t, ctx, conn, &lensv1.Subscribe{ViewId: 1, Zoom: 12, Bbox: bayBox, Mode: lensv1.Mode_MODE_HISTORY, CursorMs: time.Now().Add(-time.Hour).UnixMilli()})
	errorsSeen := 0
	end := time.Now().Add(3 * time.Second)
	for time.Now().Before(end) {
		rctx, rcancel := context.WithDeadline(ctx, end)
		_, data, err := conn.Read(rctx)
		rcancel()
		if err != nil {
			break
		}
		var m lensv1.ServerMessage
		if err := proto.Unmarshal(data, &m); err != nil {
			t.Fatal(err)
		}
		if m.GetFrame() != nil {
			t.Fatal("no frames while history fails")
		}
		if m.GetError() != nil {
			errorsSeen++
		}
	}
	if calls := st.asOfCalls.Load(); calls < 1 || calls > 3 || errorsSeen < 1 || errorsSeen > 3 {
		t.Fatalf("%d queries and %d errors in 3 s, want about 2 of each", calls, errorsSeen)
	}
}

func TestSeries(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	st := &fakeStore{series: func(view string, mmsi int64, from, to time.Time) ([]store.SeriesRow, error) {
		if mmsi != 2 {
			return nil, nil // this vessel has no data
		}
		rows := make([]store.SeriesRow, 1000)
		for i := range rows {
			rows[i] = store.SeriesRow{T: from.Add(time.Duration(i) * time.Second), Lat: 37, Lon: -122, Sog: float32(i)}
		}
		return rows, nil
	}}
	s := startServer(t, testConfig(), fleetOf(3), st)
	conn := s.dial(t, ctx, "")
	readMsg(t, ctx, conn)

	nextSeries := func() *lensv1.Series {
		t.Helper()
		for {
			if x := readMsg(t, ctx, conn).GetSeries(); x != nil {
				return x
			}
		}
	}
	writeSub(t, ctx, conn, &lensv1.Subscribe{ViewId: 1, Zoom: 12, Bbox: bayBox, SelectedMmsi: 2, SeriesWindowMs: 3600_000, SeriesMaxPoints: 100})
	x := nextSeries()
	if x.Mmsi != 2 || x.ViewId != 1 || x.Tier != lensv1.Tier_TIER_1S || x.RawPoints != 1000 || len(x.Metrics) != 5 {
		t.Fatalf("mmsi=%d view=%d tier=%v raw=%d metrics=%d", x.Mmsi, x.ViewId, x.Tier, x.RawPoints, len(x.Metrics))
	}
	for _, m := range x.Metrics {
		if len(m.Values) != 100 || len(m.TOffsetS) != 100 {
			t.Fatalf("metric %v: %d values, %d offsets", m.Metric, len(m.Values), len(m.TOffsetS))
		}
	}

	writeSub(t, ctx, conn, &lensv1.Subscribe{ViewId: 2, Zoom: 12, Bbox: bayBox, SelectedMmsi: 3, SeriesWindowMs: 3600_000})
	x = nextSeries()
	if x.Mmsi != 3 || x.RawPoints != 0 || len(x.Track) != 0 {
		t.Fatalf("no data: %v", x)
	}
	for _, m := range x.Metrics {
		if len(m.Values) != 0 || len(m.TOffsetS) != 0 {
			t.Fatalf("no data must give empty arrays: %v", m)
		}
	}
}

// Series and frames keep flowing for a selected vessel while clustered, without any telemetry.
func TestSeriesContinueWhileClustered(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	st := &fakeStore{series: func(string, int64, time.Time, time.Time) ([]store.SeriesRow, error) {
		return []store.SeriesRow{{T: time.Now(), Lat: 37, Lon: -122}}, nil
	}}
	s := startServer(t, testConfig(), twoHarbours(), st)
	conn := s.dial(t, ctx, "")
	readMsg(t, ctx, conn)
	writeSub(t, ctx, conn, &lensv1.Subscribe{ViewId: 1, Zoom: 5, Bbox: coastBox, SelectedMmsi: 2, SeriesWindowMs: 3600_000})
	var sawSeries, sawFrame bool
	for !sawSeries || !sawFrame {
		m := readMsg(t, ctx, conn)
		sawSeries = sawSeries || m.GetSeries() != nil
		if f := m.GetFrame(); f != nil {
			sawFrame = true
			if !f.Clustered || len(f.Vessels) != 0 {
				t.Fatalf("got %v", f)
			}
		}
	}
}

// The stats handler runs next to every kind of traffic: this is mostly here for the race detector.
func TestStatsDuringTraffic(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	world := fleetOf(50)
	s := startServer(t, testConfig(), world, nil)
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		conn := s.dial(t, ctx, "")
		readMsg(t, ctx, conn)
		wg.Add(1)
		go func(i int) { // no t.Fatal in here: this is not the test's goroutine
			defer wg.Done()
			for view := uint32(1); view < 15; view++ {
				data, err := proto.Marshal(&lensv1.ClientMessage{Msg: &lensv1.ClientMessage_Subscribe{Subscribe: &lensv1.Subscribe{
					ViewId: view, Zoom: float32(3 + (int(view)+i)%10), Bbox: bayBox, Firehose: view%5 == 0,
				}}})
				if err != nil || conn.Write(ctx, websocket.MessageBinary, data) != nil {
					return
				}
				for { // until this view is echoed
					_, raw, err := conn.Read(ctx)
					if err != nil {
						return
					}
					var m lensv1.ServerMessage
					if proto.Unmarshal(raw, &m) != nil {
						return
					}
					if f := m.GetFrame(); f != nil && f.ViewId == view {
						break
					}
				}
			}
		}(i)
	}
	finished := make(chan struct{})
	go func() {
		wg.Wait()
		close(finished)
	}()
	for hits := 1; ; hits++ {
		select {
		case <-finished:
			if ctx.Err() != nil {
				t.Fatal("the clients did not get through their views")
			}
			return
		default:
		}
		if st := s.stats(t); st.Vessels != 50 {
			t.Fatalf("vessels %d", st.Vessels)
		}
		world.Upsert(model.VesselState{MMSI: 1, TsMs: int64(hits) + 10, Lat: 37.8, Lon: -122.5, Name: "V"})
	}
}
