// Command loadgen opens many WebSocket clients against the gateway and reports what they received.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"sync"
	"time"

	"github.com/coder/websocket"
	"google.golang.org/protobuf/proto"

	lensv1 "github.com/portlink/lens/server/internal/gen/lens/v1"
)

const panEvery = 2 * time.Second

type options struct {
	url              string
	duration, warmup time.Duration
	zoomMin, zoomMax float64
	firehose         bool
}

type result struct {
	connected     bool
	bytes, frames uint64  // counted after the warmup only
	seconds       float64 // how long this client was measured
	dropped       uint32  // ticks dropped after the warmup
	serverErrors  int
	err           error // set if the client ended before the run did
}

func main() {
	var o options
	flag.StringVar(&o.url, "url", "ws://localhost:8080/ws", "gateway WebSocket URL")
	clients := flag.Int("clients", 50, "number of clients")
	flag.DurationVar(&o.duration, "duration", 30*time.Second, "how long to measure")
	flag.DurationVar(&o.warmup, "warmup", 0, "run this long first and leave it out of the numbers: a comparison of lens and firehose needs at least 30s")
	flag.Float64Var(&o.zoomMin, "zoom-min", 7, "lowest zoom a client picks")
	flag.Float64Var(&o.zoomMax, "zoom-max", 12, "highest zoom a client picks")
	flag.BoolVar(&o.firehose, "firehose", false, "subscribe to the firehose baseline")
	flag.Parse()

	if *clients <= 0 || o.duration <= 0 || o.warmup < 0 || o.zoomMin > o.zoomMax {
		fmt.Fprintln(os.Stderr, "loadgen: need -clients > 0, -duration > 0, -warmup >= 0 and -zoom-min <= -zoom-max")
		os.Exit(2)
	}

	ctx, cancel := context.WithTimeout(context.Background(), o.warmup+o.duration)
	defer cancel()
	results := make([]result, *clients)
	var wg sync.WaitGroup
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i] = runClient(ctx, i, o)
		}(i)
	}
	wg.Wait()

	if !report(results, o) {
		os.Exit(1)
	}
}

func runClient(ctx context.Context, i int, o options) (res result) {
	dctx, dcancel := context.WithTimeout(ctx, 10*time.Second)
	defer dcancel()
	// No Origin header: the gateway allows a missing one, and only a foreign one gets 403.
	conn, _, err := websocket.Dial(dctx, o.url, nil)
	if err != nil {
		res.err = err
		return res
	}
	defer conn.CloseNow()
	conn.SetReadLimit(8 << 20)
	res.connected = true

	// Fixed seed per client, so two runs look at the same places and can be compared.
	r := rand.New(rand.NewSource(int64(i) + 1))
	lat, lon := 33+r.Float64()*15, -124+r.Float64()*6
	zoom := o.zoomMin + r.Float64()*(o.zoomMax-o.zoomMin)
	degPerPx := 360 / (512 * math.Pow(2, zoom))
	halfW, halfH := 700*degPerPx, 400*degPerPx*math.Cos(lat*math.Pi/180)

	var view uint32
	subscribe := func() error {
		view++
		data, err := proto.Marshal(&lensv1.ClientMessage{Msg: &lensv1.ClientMessage_Subscribe{Subscribe: &lensv1.Subscribe{
			ViewId: view, Zoom: float32(zoom), Mode: lensv1.Mode_MODE_LIVE, Firehose: o.firehose,
			Bbox: &lensv1.BBox{
				MinLon: max(-180, lon-halfW), MinLat: max(-85, lat-halfH),
				MaxLon: min(180, lon+halfW), MaxLat: min(85, lat+halfH),
			},
		}}})
		if err != nil {
			return err
		}
		return conn.Write(ctx, websocket.MessageBinary, data)
	}
	if err := subscribe(); err != nil {
		res.err = err
		return res
	}

	// Pan like a user: shift the box by 10% every 2 s, turning back now and then to stay near the coast.
	go func() {
		t := time.NewTicker(panEvery)
		defer t.Stop()
		for n := 0; ; n++ {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
			dir := 1.0
			if n%20 >= 10 {
				dir = -1
			}
			lon += dir * 0.2 * halfW
			lat += dir * 0.2 * halfH
			if subscribe() != nil {
				return
			}
		}
	}()

	measureFrom := time.Now().Add(o.warmup)
	var droppedBefore uint32 // what the warmup already dropped
	for {
		typ, data, err := conn.Read(ctx)
		if err != nil {
			if ctx.Err() == nil {
				res.err = err
			}
			res.seconds = time.Since(measureFrom).Seconds()
			return res
		}
		if typ != websocket.MessageBinary {
			continue
		}
		var m lensv1.ServerMessage
		if err := proto.Unmarshal(data, &m); err != nil {
			res.err = err
			res.seconds = time.Since(measureFrom).Seconds()
			return res
		}
		counting := !time.Now().Before(measureFrom)
		if counting {
			res.bytes += uint64(len(data))
		}
		switch x := m.Msg.(type) {
		case *lensv1.ServerMessage_Frame:
			if counting {
				res.frames++
				res.dropped = x.Frame.DroppedTotal - droppedBefore
			} else {
				droppedBefore = x.Frame.DroppedTotal
			}
		case *lensv1.ServerMessage_Error:
			res.serverErrors++
		}
	}
}

// vesselCount asks the gateway's stats endpoint on the same host, -1 if that fails.
func vesselCount(wsURL string) int {
	u, err := url.Parse(wsURL)
	if err != nil {
		return -1
	}
	if u.Scheme == "wss" {
		u.Scheme = "https"
	} else {
		u.Scheme = "http"
	}
	u.Path, u.RawQuery = "/api/stats", ""
	client := http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(u.String())
	if err != nil {
		return -1
	}
	defer resp.Body.Close()
	var stats struct {
		Vessels int `json:"vessels"`
	}
	if resp.StatusCode != http.StatusOK || json.NewDecoder(resp.Body).Decode(&stats) != nil {
		return -1
	}
	return stats.Vessels
}

// report prints the result table. It returns false if no client could connect.
func report(results []result, o options) bool {
	connected, endedEarly, serverErrors := 0, 0, 0
	var totalBps, totalFps float64 // each client divided by its own measured time
	var maxDropped uint32
	var firstErr error
	for _, r := range results {
		if r.connected {
			connected++
			if r.err != nil {
				endedEarly++
			}
		}
		if r.seconds > 0 {
			totalBps += float64(r.bytes) / r.seconds
			totalFps += float64(r.frames) / r.seconds
		}
		maxDropped = max(maxDropped, r.dropped)
		serverErrors += r.serverErrors
		if firstErr == nil {
			firstErr = r.err
		}
	}
	if firstErr != nil {
		fmt.Fprintln(os.Stderr, "loadgen: first client error:", firstErr)
	}
	if connected == 0 {
		fmt.Fprintf(os.Stderr, "loadgen: none of %d clients could connect to %s\n", len(results), o.url)
		return false
	}
	mode := "lens"
	if o.firehose {
		mode = "firehose"
	}
	fmt.Printf("%-8s %-9s %8s %12s %14s %15s %11s %8s\n", "clients", "mode", "seconds", "total B/s", "B/s per client", "frames/s/client", "max dropped", "vessels")
	fmt.Printf("%-8d %-9s %8.0f %12.0f %14.0f %15.1f %11d %8d\n",
		connected, mode, o.duration.Seconds(), totalBps, totalBps/float64(connected),
		totalFps/float64(connected), maxDropped, vesselCount(o.url))
	if connected < len(results) {
		fmt.Printf("%d of %d clients could not connect\n", len(results)-connected, len(results))
	}
	if endedEarly > 0 {
		fmt.Printf("%d of %d clients ended early\n", endedEarly, connected)
	}
	if serverErrors > 0 {
		fmt.Printf("the server sent %d error messages\n", serverErrors)
	}
	return true
}
