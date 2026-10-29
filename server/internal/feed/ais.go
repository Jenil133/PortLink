package feed

import (
	"bufio"
	"cmp"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"os"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	lensv1 "github.com/portlink/lens/server/internal/gen/lens/v1"
	"github.com/portlink/lens/server/internal/model"
)

const (
	headingUnknown = 511   // AIS code for "not available"
	sogUnavailable = 102.2 // AIS sends 102.3 for "not available", 102.2 for "102.2 or faster"
	maxGapSeconds  = 600   // a longer silence means out of coverage: do not invent a path across it
)

var aisTimeLayouts = []string{"2006-01-02T15:04:05", "2006-01-02 15:04:05", time.RFC3339}

// fix is one position report. A day has millions of them, hence float32 and no strings.
type fix struct {
	t                 float32 // seconds since 00:00 UTC of the file's day
	lat, lon          float32
	sog, cog, heading float32
}

type aisTrack struct {
	mmsi  uint32
	name  string
	vtype uint32
	fixes []fix // sorted by t, strictly increasing
}

// AISReplay replays one day of real AIS tracks, mapped onto the wall clock.
type AISReplay struct {
	tracks []aisTrack
	speed  float64
	fanout int
}

// aisColumns holds the position of each column in a row, -1 when an optional one is absent.
type aisColumns struct {
	mmsi, time, lat, lon, sog, cog, heading, name, vtype int
}

// resolveAISColumns accepts both NOAA header variants by matching names case-insensitively.
func resolveAISColumns(header []string) (aisColumns, error) {
	col := make(map[string]int, len(header))
	for i, h := range header {
		col[strings.ToLower(strings.TrimSpace(strings.TrimPrefix(h, "\xef\xbb\xbf")))] = i
	}
	find := func(aliases ...string) int {
		for _, a := range aliases {
			if i, ok := col[a]; ok {
				return i
			}
		}
		return -1
	}
	c := aisColumns{
		mmsi: find("mmsi"), time: find("basedatetime", "base_date_time"),
		lat: find("lat", "latitude"), lon: find("lon", "longitude"),
		sog: find("sog"), cog: find("cog"), heading: find("heading"),
		name: find("vesselname", "vessel_name"), vtype: find("vesseltype", "vessel_type"),
	}
	for _, r := range []struct {
		name string
		pos  int
	}{{"mmsi", c.mmsi}, {"basedatetime or base_date_time", c.time}, {"lat or latitude", c.lat}, {"lon or longitude", c.lon}} {
		if r.pos < 0 {
			return c, fmt.Errorf("AIS header has no %s column, saw: %s", r.name, strings.Join(header, ","))
		}
	}
	return c, nil
}

func aisField(rec []string, i int) string {
	if i < 0 || i >= len(rec) {
		return ""
	}
	return rec[i]
}

func parseFinite(s string) (float64, bool) {
	v, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
		return 0, false
	}
	return v, true
}

func parseSOG(s string) float32 {
	if v, ok := parseFinite(s); ok && v >= 0 && v < sogUnavailable {
		return float32(v)
	}
	return 0
}

// parseCOG treats 360 and anything else out of range as "not available".
func parseCOG(s string) float32 {
	if v, ok := parseFinite(s); ok && v >= 0 && v < 360 {
		return float32(v)
	}
	return 0
}

// parseHeading keeps "not available" apart from a real 0, because a vessel with no heading is
// drawn along its course. The legacy files send 511, the csv.zst files leave the field blank.
func parseHeading(s string) float32 {
	if v, ok := parseFinite(s); ok && v >= 0 && v < 360 {
		return float32(v)
	}
	return headingUnknown
}

func parseSecondsOfDay(s string) (float32, bool) {
	s = strings.TrimSpace(s)
	for _, layout := range aisTimeLayouts {
		if t, err := time.Parse(layout, s); err == nil {
			t = t.UTC()
			return float32(t.Hour()*3600 + t.Minute()*60 + t.Second()), true
		}
	}
	return 0, false
}

// LoadAIS reads one MarineCadastre daily CSV and keeps the reports inside box.
func LoadAIS(path string, box model.BBox, speed float64, fanout int) (*AISReplay, error) {
	started := time.Now()
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open AIS file %s: %w (make fetch-ais downloads one)", path, err)
	}
	defer f.Close()

	r := csv.NewReader(bufio.NewReaderSize(f, 1<<20))
	r.ReuseRecord, r.FieldsPerRecord, r.LazyQuotes = true, -1, true
	header, err := r.Read()
	if err != nil {
		return nil, fmt.Errorf("read AIS header of %s: %w", path, err)
	}
	cols, err := resolveAISColumns(header)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}

	byMMSI := map[uint32]*aisTrack{}
	skipped, outside := 0, 0
	for {
		rec, err := r.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			var pe *csv.ParseError
			if errors.As(err, &pe) {
				skipped++
				continue
			}
			return nil, fmt.Errorf("read %s: %w", path, err)
		}
		// Position first: most rows of a whole-US file are outside the box, so this is the cheap exit.
		lat, okLat := parseFinite(aisField(rec, cols.lat))
		lon, okLon := parseFinite(aisField(rec, cols.lon))
		if !okLat || !okLon || lat < -90 || lat > 90 || lon < -180 || lon > 180 {
			skipped++
			continue
		}
		if !box.Contains(lat, lon) {
			outside++
			continue
		}
		mmsi, err := strconv.ParseUint(strings.TrimSpace(aisField(rec, cols.mmsi)), 10, 32)
		if err != nil || mmsi == 0 || mmsi >= 1_000_000_000 {
			skipped++
			continue
		}
		sec, ok := parseSecondsOfDay(aisField(rec, cols.time))
		if !ok {
			skipped++
			continue
		}
		tr := byMMSI[uint32(mmsi)]
		if tr == nil {
			tr = &aisTrack{mmsi: uint32(mmsi)}
			byMMSI[uint32(mmsi)] = tr
		}
		if tr.name == "" {
			// Clone: a substring would keep the whole row's string alive for the rest of the run.
			// Valid UTF-8 only: protobuf refuses anything else, which would stop the whole feed.
			tr.name = strings.Clone(strings.ToValidUTF8(strings.TrimSpace(aisField(rec, cols.name)), ""))
		}
		if tr.vtype == 0 {
			if v, ok := parseFinite(aisField(rec, cols.vtype)); ok && v > 0 && v < 1000 {
				tr.vtype = uint32(v)
			}
		}
		tr.fixes = append(tr.fixes, fix{
			t: sec, lat: float32(lat), lon: float32(lon),
			sog: parseSOG(aisField(rec, cols.sog)), cog: parseCOG(aisField(rec, cols.cog)),
			heading: parseHeading(aisField(rec, cols.heading)),
		})
	}

	tracks := make([]aisTrack, 0, len(byMMSI))
	totalFixes := 0
	for _, tr := range byMMSI {
		slices.SortStableFunc(tr.fixes, func(a, b fix) int { return cmp.Compare(a.t, b.t) })
		tr.fixes = slices.CompactFunc(tr.fixes, func(a, b fix) bool { return a.t == b.t })
		if len(tr.fixes) < 2 {
			continue
		}
		totalFixes += len(tr.fixes)
		tracks = append(tracks, *tr)
	}
	if len(tracks) == 0 {
		return nil, fmt.Errorf("no tracks in bbox %+v in %s (%d rows outside it, %d skipped)", box, path, outside, skipped)
	}
	// Sorted so that vessel index i means the same vessel across restarts.
	slices.SortFunc(tracks, func(a, b aisTrack) int { return cmp.Compare(a.mmsi, b.mmsi) })

	fanout = min(4, max(1, fanout)) // the MMSI offset of a clone must fit in uint32
	if !(speed >= 0.1) {
		speed = 0.1
	}
	speed = min(100, speed)
	slog.Info("AIS file loaded", "path", path, "tracks", len(tracks), "fixes", totalFixes,
		"rows_skipped", skipped, "rows_outside_bbox", outside, "speed", speed, "fanout", fanout,
		"took", time.Since(started).Round(time.Millisecond))
	return &AISReplay{tracks: tracks, speed: speed, fanout: fanout}, nil
}

func (a *AISReplay) Len() int { return len(a.tracks) * a.fanout }

// SampleAt maps wall-clock time onto the file's 24 hours:
//
//	virtual seconds-of-day = (unix seconds * speed) mod 86400
//
// At speed 1 the replay shows what happened at the same UTC time of day on the recorded day.
// It is a pure function of (i, t), so backfill and live agree and restarts leave no seam.
func (a *AISReplay) SampleAt(i int, t time.Time) (*lensv1.VesselSample, bool) {
	n := len(a.tracks)
	tr := &a.tracks[i%n]
	clone := i / n
	v := float32(math.Mod(float64(t.UnixMilli())/1000*a.speed, 86400))

	// j = index of the first fix after v. We need one fix on each side to interpolate.
	j := sort.Search(len(tr.fixes), func(k int) bool { return tr.fixes[k].t > v })
	if j == 0 || j == len(tr.fixes) {
		return nil, false // before the vessel's first report or after its last
	}
	p, q := tr.fixes[j-1], tr.fixes[j]
	if q.t-p.t > maxGapSeconds {
		return nil, false
	}
	k := (v - p.t) / (q.t - p.t)
	lat := float64(p.lat + (q.lat-p.lat)*k)
	lon := float64(p.lon + (q.lon-p.lon)*k)
	heading := p.heading
	if heading >= 360 {
		heading = p.cog
	}
	// Clones get a distinct MMSI (real MMSIs have 9 digits, so adding multiples of 1e9 never
	// collides and still fits in uint32 for clone <= 3) and a small position offset. The offset
	// must not leave the valid range: the database would refuse the whole batch holding such a row.
	mmsi := tr.mmsi + uint32(clone)*1_000_000_000
	lat = min(90, lat+0.02*float64(clone))
	if lon += 0.03 * float64(clone); lon > 180 {
		lon -= 360
	}
	name := tr.name
	if clone > 0 && name != "" {
		name = fmt.Sprintf("%s #%d", tr.name, clone+1)
	}
	return &lensv1.VesselSample{
		Mmsi: mmsi, TsMs: t.UnixMilli(), Lat: lat, Lon: lon,
		SogKn: p.sog, CogDeg: p.cog, HeadingDeg: heading, Name: name, VesselType: tr.vtype,
	}, true
}
