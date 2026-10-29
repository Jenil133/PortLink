package gateway

import (
	"context"
	"math"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
	"google.golang.org/protobuf/proto"

	lensv1 "github.com/portlink/lens/server/internal/gen/lens/v1"
	"github.com/portlink/lens/server/internal/model"
)

const (
	probeEvery          = time.Second // how often delivery is confirmed
	defaultProbeTimeout = time.Minute // no answer for this long: the client is gone
	maxUnconfirmedMs    = 500         // this much slower than the link's own round trip: the client is behind

	maxSubscribesPerSec = 40   // a throttled UI sends at most about 30 per second
	historyRetryMs      = 2000 // while history fails: one query, and one error message, this often
)

// How a client's frames are built. A change invalidates everything the client holds.
const (
	renderLens = iota + 1
	renderClustered
	renderFirehose
)

func renderOf(sub *lensv1.Subscribe, clusterZoom float64) int {
	switch {
	case sub.Firehose:
		return renderFirehose
	case float64(sub.Zoom) < clusterZoom:
		return renderClustered
	}
	return renderLens
}

type client struct {
	conn *websocket.Conn
	srv  *Server
	id   uint64
	slow time.Duration // debug: pause after every frame, simulating a client that reads slowly

	// Shared between the reader and the writer.
	mu      sync.Mutex
	sub     *lensv1.Subscribe
	subAtMs int64 // server time when sub arrived, base for the playback cursor

	wake       chan struct{} // capacity 1: the bounded send buffer
	dropped    atomic.Uint32 // ticks that found the buffer full, or the client behind
	born       time.Time     // start of the probe's clock
	probeAtMs  atomic.Int64  // elapsedMs when the ping in flight was sent, 0 when none is
	probeMinMs atomic.Int64  // the fastest answer so far: the link's own round trip, -1 before the first

	// For /api/stats.
	bytesOut, framesOut atomic.Uint64
	inView              atomic.Uint32

	// Owned by the reader goroutine only: Subscribes counted in the current second.
	rlWindow int64
	rlCount  int

	// Owned by the writer goroutine only. No locking needed.
	sent       map[uint32]qState
	gen        uint32
	seq        uint32
	started    bool
	render     int
	lastSendMs int64
	lastViewID uint32 // view_id of the last frame sent
	lastErrMs  int64
	hist       *historySource
	scratch    []model.VesselState
	seriesKey  seriesKey
	seriesAtMs int64
}

func newClient(conn *websocket.Conn, srv *Server) *client {
	c := &client{
		conn: conn, srv: srv, born: time.Now(),
		wake: make(chan struct{}, 1),
		sent: make(map[uint32]qState),
		hist: &historySource{st: srv.st},
	}
	c.probeMinMs.Store(-1)
	return c
}

func (c *client) run(root context.Context) {
	// Not a child of root: cancelling the context of a read or write drops the socket at once, before
	// a close frame could go out. On shutdown the client closes with 1001 first and cancels after that.
	ctx, cancel := context.WithCancel(context.WithoutCancel(root))
	defer cancel()
	stop := context.AfterFunc(root, func() {
		c.conn.Close(websocket.StatusGoingAway, "server shutting down")
		cancel()
	})
	defer stop()
	defer c.conn.Close(websocket.StatusNormalClosure, "")

	// Nothing reads this socket yet, so a stuck database must not be able to hold the handshake open.
	hctx, hcancel := context.WithTimeout(ctx, 2*time.Second)
	histStart, err := c.srv.st.HistoryStart(hctx, c.srv.cfg.HistoryHours)
	hcancel()
	now := time.Now() // after the query: the client sets its clock offset from this Hello
	if err != nil {
		histStart = now // history unavailable, the scrubber will have zero range
	}
	if err := c.send(ctx, &lensv1.ServerMessage{Msg: &lensv1.ServerMessage_Hello{Hello: &lensv1.Hello{
		ProtocolVersion: 1, ServerTimeMs: now.UnixMilli(), HistoryStartMs: histStart.UnixMilli(),
		LiveTickMs: uint32(c.srv.cfg.LiveTick.Milliseconds()),
	}}}); err != nil {
		return
	}
	go c.readLoop(ctx, cancel)
	go c.tickLoop(ctx)
	go c.probeLoop(ctx, cancel)
	c.writeLoop(ctx)
}

// tickLoop offers one wake token per tick. If the previous token has not been consumed, the
// writer is behind: count a drop and move on. Nothing ever queues, which is the whole point.
func (c *client) tickLoop(ctx context.Context) {
	t := time.NewTicker(c.srv.cfg.LiveTick)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if c.behind() {
				c.dropped.Add(1)
				continue
			}
			select {
			case c.wake <- struct{}{}:
			default:
				c.dropped.Add(1)
			}
		}
	}
}

// probeLoop keeps one WebSocket ping in flight. Socket buffers hide a slow reader: a write returns
// as soon as the kernel has the bytes, and hundreds of KB of stale frames can pile up before a
// write ever blocks. A ping is answered only after everything written before it has been read,
// end to end and through any proxy, so while one is overdue the client counts as behind. Ticks
// are then dropped here instead of queueing there, and the next frame jumps to the latest state.
//
// "Overdue" is measured against the fastest answer so far, which is the link's own round trip:
// a distant client that keeps up is not behind. The first ping goes out straight after Hello, when
// nothing can be queued ahead of it.
func (c *client) probeLoop(ctx context.Context, cancel context.CancelFunc) {
	defer cancel()
	t := time.NewTicker(probeEvery)
	defer t.Stop()
	fastest := int64(math.MaxInt64)
	for {
		sent := c.elapsedMs()
		c.probeAtMs.Store(sent)
		pctx, pcancel := context.WithTimeout(ctx, c.srv.probeTimeout)
		err := c.conn.Ping(pctx)
		pcancel()
		c.probeAtMs.Store(0)
		if err != nil {
			return
		}
		fastest = min(fastest, c.elapsedMs()-sent)
		c.probeMinMs.Store(fastest)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (c *client) behind() bool {
	at, fastest := c.probeAtMs.Load(), c.probeMinMs.Load()
	if at == 0 || fastest < 0 { // nothing in flight, or the first answer has yet to show what this link can do
		return false
	}
	return c.elapsedMs()-at > maxUnconfirmedMs+fastest
}

// elapsedMs is the probe's clock. It is monotonic, because wall time can jump, and never 0, because
// probeAtMs uses 0 for "no ping in flight".
func (c *client) elapsedMs() int64 { return time.Since(c.born).Milliseconds() + 1 }

func (c *client) readLoop(ctx context.Context, cancel context.CancelFunc) {
	defer cancel()
	for {
		typ, data, err := c.conn.Read(ctx)
		if err != nil {
			return
		}
		if typ != websocket.MessageBinary {
			continue
		}
		var msg lensv1.ClientMessage
		if err := proto.Unmarshal(data, &msg); err != nil {
			c.sendError(ctx, 1, "cannot decode message")
			continue
		}
		switch m := msg.Msg.(type) {
		case *lensv1.ClientMessage_Subscribe:
			sec := time.Now().Unix()
			if sec != c.rlWindow {
				c.rlWindow, c.rlCount = sec, 0
			}
			if c.rlCount++; c.rlCount > maxSubscribesPerSec {
				if c.rlCount == maxSubscribesPerSec+1 {
					c.sendError(ctx, 4, "too many subscribes")
				}
				continue
			}
			if reason := normalize(m.Subscribe, c.srv.cfg.HistoryHours); reason != "" {
				c.sendError(ctx, 2, reason)
				continue
			}
			c.mu.Lock()
			c.sub, c.subAtMs = m.Subscribe, time.Now().UnixMilli()
			c.mu.Unlock()
			// Nudge the writer so a seek is answered immediately instead of at the next tick.
			// Not counted as a drop if the buffer is already full. A client that is behind
			// gets the new view with the first frame after it catches up.
			if !c.behind() {
				select {
				case c.wake <- struct{}{}:
				default:
				}
			}
		case *lensv1.ClientMessage_Ping:
			_ = c.send(ctx, &lensv1.ServerMessage{Msg: &lensv1.ServerMessage_Pong{Pong: &lensv1.Pong{
				ClientTimeMs: m.Ping.ClientTimeMs, ServerTimeMs: time.Now().UnixMilli(),
			}}})
		}
	}
}

func (c *client) writeLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-c.wake:
		}
		c.mu.Lock()
		sub, subAt := c.sub, c.subAtMs
		c.mu.Unlock()
		if sub == nil {
			continue
		}
		now := time.Now().UnixMilli()

		frame, err := c.buildFrame(ctx, sub, subAt, now)
		// Stamps below read the clock again: the query may have taken up to 2 s.
		if err != nil {
			c.sendError(ctx, 3, "history unavailable")
			c.lastErrMs = time.Now().UnixMilli()
		} else if frame != nil {
			if err := c.send(ctx, &lensv1.ServerMessage{Msg: &lensv1.ServerMessage_Frame{Frame: frame}}); err != nil {
				return // write error or 5 s timeout: this client is gone
			}
			c.lastSendMs = time.Now().UnixMilli()
			c.framesOut.Add(1)
			c.inView.Store(frame.InView)
			if c.slow > 0 {
				// No queue: ticks keep arriving, the wake buffer stays full, drops are counted,
				// and the next frame is still computed from the latest state.
				select {
				case <-ctx.Done():
					return
				case <-time.After(c.slow):
				}
			}
		}

		if s := c.maybeBuildSeries(ctx, sub, now); s != nil {
			if err := c.send(ctx, &lensv1.ServerMessage{Msg: &lensv1.ServerMessage_Series{Series: s}}); err != nil {
				return
			}
		}
	}
}

// buildFrame diffs "what the client should see now" against "what the client holds".
// It returns nil when there is nothing worth sending.
func (c *client) buildFrame(ctx context.Context, sub *lensv1.Subscribe, subAtMs, nowMs int64) (*lensv1.Frame, error) {
	render := renderOf(sub, c.srv.cfg.ClusterZoom)
	firehose := render == renderFirehose
	// Clusters change slowly, so one full list per second is plenty and needs no diffing. Deciding
	// here saves the scan of the world or the query below on the other nine ticks of each second.
	if render == renderClustered && c.started && render == c.render && nowMs-c.lastSendMs < 1000 {
		return nil, nil
	}

	view := View{
		BBox: model.BBox{MinLon: sub.Bbox.MinLon, MinLat: sub.Bbox.MinLat, MaxLon: sub.Bbox.MaxLon, MaxLat: sub.Bbox.MaxLat},
		Zoom: float64(sub.Zoom),
		All:  firehose,
	}
	var src Source = c.srv.live
	if sub.Mode == lensv1.Mode_MODE_HISTORY {
		// A failing database gets one try per client every 2 s instead of one on every tick.
		if nowMs-c.lastErrMs < historyRetryMs {
			return nil, nil
		}
		// The server advances the cursor during playback: base cursor + elapsed wall time * rate.
		cursor := sub.CursorMs + int64(float64(nowMs-subAtMs)*float64(sub.PlaybackRate))
		lo := nowMs - int64(c.srv.cfg.HistoryHours)*3600*1000
		hi := nowMs - 2000 // never ahead of what has been ingested
		view.CursorMs = min(hi, max(lo, cursor))
		src = c.hist
	}

	states, tier, tsMs, err := src.Snapshot(ctx, view, c.scratch[:0])
	c.scratch = states
	if err != nil {
		return nil, err
	}

	frame := &lensv1.Frame{ViewId: sub.ViewId, TsMs: tsMs, Tier: tier, InView: uint32(len(states))}
	// The first frame, and any change of render mode, invalidates everything the client holds.
	if !c.started || render != c.render {
		frame.Snapshot = true
		clear(c.sent)
		c.started, c.render = true, render
	}

	if render == renderClustered {
		frame.Clustered = true
		frame.Clusters = clusterStates(states, clusterRes(float64(sub.Zoom)))
		c.finishFrame(frame)
		return frame, nil
	}

	posThr := posThresholdE6(float64(sub.Zoom))
	c.gen++
	for i := range states {
		v := &states[i]
		cur := quantize(v)
		var prev *qState
		if p, ok := c.sent[v.MMSI]; ok {
			prev = &p
		}
		d, next := diffVessel(v.MMSI, prev, cur, posThr, firehose || v.MMSI == sub.SelectedMmsi, firehose)
		next.gen = c.gen
		c.sent[v.MMSI] = next
		if d != nil {
			frame.Vessels = append(frame.Vessels, d)
		}
	}
	// Anything the client holds that was not seen in this pass has left the view.
	for mmsi, st := range c.sent {
		if st.gen != c.gen {
			frame.Removed = append(frame.Removed, mmsi)
			delete(c.sent, mmsi)
		}
	}

	// Heartbeat: an empty frame at least once a second keeps ts_ms and the drop counter fresh.
	// A new view_id is answered at once even with an empty diff, otherwise the client's seek
	// latency would measure the wait for the next heartbeat instead of the seek.
	if len(frame.Vessels) == 0 && len(frame.Removed) == 0 && !frame.Snapshot &&
		sub.ViewId == c.lastViewID && nowMs-c.lastSendMs < 1000 {
		return nil, nil
	}
	c.finishFrame(frame)
	return frame, nil
}

func (c *client) finishFrame(frame *lensv1.Frame) {
	c.lastViewID = frame.ViewId
	frame.Seq = c.seq
	c.seq++
	frame.DroppedTotal = c.dropped.Load()
}

func (c *client) send(ctx context.Context, m *lensv1.ServerMessage) error {
	data, err := proto.Marshal(m)
	if err != nil {
		return err
	}
	wctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := c.conn.Write(wctx, websocket.MessageBinary, data); err != nil {
		return err
	}
	c.bytesOut.Add(uint64(len(data)))
	return nil
}

func (c *client) sendError(ctx context.Context, code uint32, msg string) {
	_ = c.send(ctx, &lensv1.ServerMessage{Msg: &lensv1.ServerMessage_Error{Error: &lensv1.ServerError{Code: code, Message: msg}}})
}
