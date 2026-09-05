package gateway

import (
	"context"
	"math"
	"sync"
	"time"

	"github.com/coder/websocket"
	"google.golang.org/protobuf/proto"

	lensv1 "github.com/portlink/lens/server/internal/gen/lens/v1"
	"github.com/portlink/lens/server/internal/model"
)

type client struct {
	conn *websocket.Conn
	srv  *Server

	mu  sync.Mutex
	sub *lensv1.Subscribe // latest subscription, nil until the first Subscribe arrives

	seq uint32
}

func (c *client) run(parent context.Context) {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	defer c.conn.Close(websocket.StatusNormalClosure, "")

	now := time.Now().UnixMilli()
	if err := c.send(ctx, &lensv1.ServerMessage{Msg: &lensv1.ServerMessage_Hello{Hello: &lensv1.Hello{
		ProtocolVersion: 1, ServerTimeMs: now, HistoryStartMs: now, LiveTickMs: 1000,
	}}}); err != nil {
		return
	}

	go c.readLoop(ctx, cancel)

	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	var scratch []model.VesselState
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.mu.Lock()
			sub := c.sub
			c.mu.Unlock()
			if sub == nil || sub.Bbox == nil {
				continue // nothing to send until the client says what it can see
			}
			box := model.BBox{MinLon: sub.Bbox.MinLon, MinLat: sub.Bbox.MinLat, MaxLon: sub.Bbox.MaxLon, MaxLat: sub.Bbox.MaxLat}
			scratch = c.srv.world.Query(box, false, scratch[:0])
			frame := &lensv1.Frame{
				ViewId: sub.ViewId, Seq: c.seq, TsMs: time.Now().UnixMilli(),
				Snapshot: true, Tier: lensv1.Tier_TIER_LIVE, InView: uint32(len(scratch)),
			}
			c.seq++
			for i := range scratch {
				v := &scratch[i]
				frame.Vessels = append(frame.Vessels, &lensv1.VesselDelta{
					Mmsi:       v.MMSI,
					LatE6:      proto.Int32(int32(math.Round(v.Lat * 1e6))),
					LonE6:      proto.Int32(int32(math.Round(v.Lon * 1e6))),
					SogDkn:     proto.Uint32(uint32(math.Round(float64(v.Sog) * 10))),
					CogDdeg:    proto.Uint32(uint32(math.Round(float64(v.Cog) * 10))),
					HeadingDeg: proto.Uint32(uint32(math.Round(float64(v.Heading)))),
					Name:       proto.String(v.Name),
					VesselType: proto.Uint32(v.VesselType),
				})
			}
			if err := c.send(ctx, &lensv1.ServerMessage{Msg: &lensv1.ServerMessage_Frame{Frame: frame}}); err != nil {
				return
			}
		}
	}
}

func (c *client) readLoop(ctx context.Context, cancel context.CancelFunc) {
	defer cancel() // any read error (including a normal close) ends the connection
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
			continue // Phase 2 replies with ServerError code 1
		}
		if sub := msg.GetSubscribe(); sub != nil && validBBox(sub.Bbox) {
			c.mu.Lock()
			c.sub = sub
			c.mu.Unlock()
		}
	}
}

// send marshals and writes with a deadline so one stuck client cannot hang its goroutine forever.
func (c *client) send(ctx context.Context, m *lensv1.ServerMessage) error {
	data, err := proto.Marshal(m)
	if err != nil {
		return err
	}
	wctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return c.conn.Write(wctx, websocket.MessageBinary, data)
}

func validBBox(b *lensv1.BBox) bool {
	if b == nil {
		return false
	}
	for _, v := range []float64{b.MinLon, b.MinLat, b.MaxLon, b.MaxLat} {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return false
		}
	}
	return b.MinLon < b.MaxLon && b.MinLat < b.MaxLat &&
		b.MinLat >= -90 && b.MaxLat <= 90 && b.MinLon >= -360 && b.MaxLon <= 360
}
