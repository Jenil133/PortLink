package feed

import (
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/portlink/lens/server/internal/model"
)

var westCoast = model.BBox{MinLon: -126, MinLat: 32, MaxLon: -116, MaxLat: 49}

const legacyHeader = "MMSI,BaseDateTime,LAT,LON,SOG,COG,Heading,VesselName,IMO,CallSign,VesselType\n"
const newHeader = "mmsi,base_date_time,longitude,latitude,sog,cog,heading,vessel_name,imo,call_sign,vessel_type\n"

func writeCSV(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "ais.csv")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// atSecond returns a wall-clock time whose seconds-of-day is sec at speed 1.
func atSecond(sec int) time.Time {
	return time.Date(2030, 1, 2, 0, 0, 0, 0, time.UTC).Add(time.Duration(sec) * time.Second)
}

// Vessel 111111111 has reports at 00:00:00, 00:02:00 and 00:04:00.
// Vessel 222222222 has a 20 minute gap between its two reports.
const legacyRows = "111111111,2024-01-01T00:00:00,37.0,-122.0,10,90,511,ALPHA,,,70\n" +
	"111111111,2024-01-01T00:02:00,37.2,-122.2,12,100,95,ALPHA,,,70\n" +
	"111111111,2024-01-01T00:04:00,37.4,-122.4,14,110,105,ALPHA,,,70\n" +
	"222222222,2024-01-01T00:00:00,38.0,-123.0,5,10,10,BRAVO,,,80\n" +
	"222222222,2024-01-01T00:20:00,38.5,-123.5,5,10,10,BRAVO,,,80\n" +
	"333333333,2024-01-01T00:00:00,38.0,-123.0,5,10,10,ONLY ONE FIX,,,80\n"

func checkReplay(t *testing.T, a *AISReplay) {
	t.Helper()
	if a.Len() != 2 {
		t.Fatalf("Len = %d, want 2: the vessel with one fix must be dropped", a.Len())
	}
	s, ok := a.SampleAt(0, atSecond(60))
	if !ok {
		t.Fatal("no sample halfway between two fixes")
	}
	if math.Abs(s.Lat-37.1) > 1e-4 || math.Abs(s.Lon+122.1) > 1e-4 {
		t.Fatalf("halfway position = %v, %v, want 37.1, -122.1", s.Lat, s.Lon)
	}
	if s.HeadingDeg != 90 {
		t.Fatalf("heading 511 must fall back to course 90, got %v", s.HeadingDeg)
	}
	if s.Name != "ALPHA" || s.VesselType != 70 || s.SogKn != 10 || s.Mmsi != 111111111 {
		t.Fatalf("unexpected fields: %v", s)
	}
	if s.TsMs != atSecond(60).UnixMilli() {
		t.Fatalf("ts_ms = %d", s.TsMs)
	}
	if _, ok := a.SampleAt(1, atSecond(600)); ok {
		t.Fatal("no sample may be invented across a 20 minute gap")
	}
	if _, ok := a.SampleAt(0, atSecond(3600)); ok {
		t.Fatal("no sample after the vessel's last report")
	}
}

func TestLoadLegacyHeader(t *testing.T) {
	a, err := LoadAIS(writeCSV(t, legacyHeader+legacyRows), westCoast, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	checkReplay(t, a)
}

// The newer files use other names and put longitude before latitude.
func TestLoadNewHeader(t *testing.T) {
	var rows strings.Builder
	for _, line := range strings.Split(strings.TrimSpace(legacyRows), "\n") {
		f := strings.Split(line, ",")
		f[2], f[3] = f[3], f[2]
		f[1] = strings.Replace(f[1], "T", " ", 1)
		rows.WriteString(strings.Join(f, ",") + "\n")
	}
	a, err := LoadAIS(writeCSV(t, newHeader+rows.String()), westCoast, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	checkReplay(t, a)
}

func TestByteOrderMarkAndHeaderCase(t *testing.T) {
	body := "\xef\xbb\xbfMmSi,BASEDATETIME,Lat,LON\n" +
		"111111111,2024-01-01T00:00:00,37.0,-122.0\n111111111,2024-01-01T00:02:00,37.2,-122.2\n"
	a, err := LoadAIS(writeCSV(t, body), westCoast, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if s, ok := a.SampleAt(0, atSecond(60)); !ok || s.Name != "" || s.SogKn != 0 {
		t.Fatalf("optional columns may be absent: ok=%v %v", ok, s)
	}
}

func TestFanoutClones(t *testing.T) {
	a, err := LoadAIS(writeCSV(t, legacyHeader+legacyRows), westCoast, 1, 2)
	if err != nil {
		t.Fatal(err)
	}
	if a.Len() != 4 {
		t.Fatalf("fanout 2 must double Len, got %d", a.Len())
	}
	orig, ok1 := a.SampleAt(0, atSecond(60))
	clone, ok2 := a.SampleAt(2, atSecond(60))
	if !ok1 || !ok2 {
		t.Fatal("missing sample")
	}
	if clone.Mmsi != orig.Mmsi+1_000_000_000 {
		t.Fatalf("clone MMSI = %d", clone.Mmsi)
	}
	if clone.Name != "ALPHA #2" || clone.Lat == orig.Lat || clone.Lon == orig.Lon {
		t.Fatalf("clone must have its own name and offset: %v", clone)
	}
}

func TestCloneStaysInRange(t *testing.T) {
	body := legacyHeader + "111111111,2024-01-01T00:00:00,89.99,179.99,1,1,1,,,,70\n" +
		"111111111,2024-01-01T00:02:00,89.99,179.99,1,1,1,,,,70\n"
	a, err := LoadAIS(writeCSV(t, body), model.BBox{MinLon: -180, MinLat: -90, MaxLon: 180, MaxLat: 90}, 1, 4)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < a.Len(); i++ {
		s, ok := a.SampleAt(i, atSecond(60))
		if !ok || s.Lat > 90 || s.Lon > 180 || s.Lon < -180 {
			t.Fatalf("clone %d left the valid range: %v", i, s)
		}
	}
}

func TestClamps(t *testing.T) {
	p := writeCSV(t, legacyHeader+legacyRows)
	if a, err := LoadAIS(p, westCoast, 1, 9); err != nil || a.Len() != 8 {
		t.Fatalf("fanout must clamp to 4: %v", err)
	}
	if a, err := LoadAIS(p, westCoast, 1, 0); err != nil || a.Len() != 2 {
		t.Fatalf("fanout must clamp to 1: %v", err)
	}
	for _, c := range []struct{ in, want float64 }{{0, 0.1}, {math.NaN(), 0.1}, {1000, 100}, {10, 10}} {
		a, err := LoadAIS(p, westCoast, c.in, 1)
		if err != nil || a.speed != c.want {
			t.Fatalf("speed %v: got %v, %v", c.in, a, err)
		}
	}
}

func TestReplaySpeed(t *testing.T) {
	a, err := LoadAIS(writeCSV(t, legacyHeader+legacyRows), westCoast, 10, 1)
	if err != nil {
		t.Fatal(err)
	}
	// 6 wall-clock seconds at 10x is 60 virtual seconds, the halfway point again.
	s, ok := a.SampleAt(0, atSecond(0).Add(6*time.Second))
	if !ok || math.Abs(s.Lat-37.1) > 1e-3 {
		t.Fatalf("ok=%v lat=%v", ok, s.GetLat())
	}
}

func TestReplayIsPure(t *testing.T) {
	a, err := LoadAIS(writeCSV(t, legacyHeader+legacyRows), westCoast, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	s1, _ := a.SampleAt(0, atSecond(77))
	s2, _ := a.SampleAt(0, atSecond(77))
	if s1.String() != s2.String() {
		t.Fatalf("%v != %v", s1, s2)
	}
}

// 102.3 knots and 360 degrees mean "not available" in AIS, and the csv.zst files leave an unknown
// heading blank, which must not read as a real 0 (every such arrow would point north).
func TestUnavailableMarkers(t *testing.T) {
	body := legacyHeader +
		"111111111,2024-01-01T00:00:00,37.0,-122.0,102.3,360,,A,,,70\n111111111,2024-01-01T00:02:00,37.2,-122.2,1,1,1,A,,,70\n" +
		"222222222,2024-01-01T00:00:00,38.0,-122.0,102.2,45,,B,,,70\n222222222,2024-01-01T00:02:00,38.2,-122.2,1,1,1,B,,,70\n"
	a, err := LoadAIS(writeCSV(t, body), westCoast, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	s, ok := a.SampleAt(0, atSecond(60))
	if !ok || s.SogKn != 0 || s.CogDeg != 0 || s.HeadingDeg != 0 {
		t.Fatalf("markers must become 0: %v", s)
	}
	s, ok = a.SampleAt(1, atSecond(60))
	if !ok || s.SogKn != 0 || s.CogDeg != 45 || s.HeadingDeg != 45 {
		t.Fatalf("blank heading must fall back to course: %v", s)
	}
}

func TestBadRowsAreSkipped(t *testing.T) {
	body := legacyHeader +
		"0,2024-01-01T00:00:00,37.0,-122.0,1,1,1,ZERO,,,70\n0,2024-01-01T00:02:00,37.0,-122.0,1,1,1,ZERO,,,70\n" +
		"1000000000,2024-01-01T00:00:00,37.0,-122.0,1,1,1,TOO BIG,,,70\n1000000000,2024-01-01T00:02:00,37.0,-122.0,1,1,1,TOO BIG,,,70\n" +
		"abc,2024-01-01T00:00:00,37.0,-122.0,1,1,1,BAD MMSI,,,70\nabc,2024-01-01T00:02:00,37.0,-122.0,1,1,1,BAD MMSI,,,70\n" +
		"111111111,not a time,37.0,-122.0,1,1,1,A,,,70\n" +
		"111111111,2024-01-01T00:00:00,NaN,-122.0,1,1,1,A,,,70\n" +
		"111111111,2024-01-01T00:00:00,91,-122.0,1,1,1,A,,,70\n" +
		"111111111,2024-01-01T00:00:00,1.0,1.0,1,1,1,OUTSIDE BOX,,,70\n" +
		"short,row\n" +
		"111111111,2024-01-01T00:00:00,37.0,-122.0,x,y,z,GOOD,,,70\n" +
		"111111111,2024-01-01T00:00:00,37.5,-122.5,2,2,2,SAME SECOND,,,70\n" +
		"111111111,2024-01-01T00:02:00,37.2,-122.2,2,2,2,GOOD,,,70\n"
	a, err := LoadAIS(writeCSV(t, body), westCoast, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if a.Len() != 1 || len(a.tracks[0].fixes) != 2 {
		t.Fatalf("want one vessel with two fixes, got %d vessels", a.Len())
	}
	s, ok := a.SampleAt(0, atSecond(0).Add(time.Second))
	if !ok || s.Name != "GOOD" || s.SogKn != 0 || math.Abs(s.Lat-37.0) > 0.01 {
		t.Fatalf("the first report of a second must win, bad numbers read as 0: %v", s)
	}
}

// A name that is not valid UTF-8 must not stop the feed: protobuf cannot marshal it.
func TestInvalidUTF8NameCanBeMarshalled(t *testing.T) {
	body := legacyHeader +
		"111111111,2024-01-01T00:00:00,37.0,-122.0,1,1,1,CAF\xc9 ONE,,,70\n" +
		"111111111,2024-01-01T00:02:00,37.2,-122.2,1,1,1,CAF\xc9 ONE,,,70\n"
	a, err := LoadAIS(writeCSV(t, body), westCoast, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	s, ok := a.SampleAt(0, atSecond(60))
	if !ok || s.Name != "CAF ONE" {
		t.Fatalf("ok=%v name=%q", ok, s.GetName())
	}
	if _, err := proto.Marshal(s); err != nil {
		t.Fatal(err)
	}
}

func TestUnsortedInput(t *testing.T) {
	body := legacyHeader +
		"111111111,2024-01-01T00:02:00,37.2,-122.2,1,1,1,A,,,70\n" +
		"111111111,2024-01-01T00:00:00,37.0,-122.0,1,1,1,A,,,70\n"
	a, err := LoadAIS(writeCSV(t, body), westCoast, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if s, ok := a.SampleAt(0, atSecond(60)); !ok || math.Abs(s.Lat-37.1) > 1e-4 {
		t.Fatalf("ok=%v %v", ok, s)
	}
}

func TestStableOrderAcrossLoads(t *testing.T) {
	p := writeCSV(t, legacyHeader+legacyRows)
	for run := 0; run < 5; run++ {
		a, err := LoadAIS(p, westCoast, 1, 1)
		if err != nil {
			t.Fatal(err)
		}
		if a.tracks[0].mmsi != 111111111 || a.tracks[1].mmsi != 222222222 {
			t.Fatalf("vessel index must be stable: %d, %d", a.tracks[0].mmsi, a.tracks[1].mmsi)
		}
	}
}

func TestLoadErrors(t *testing.T) {
	if _, err := LoadAIS(filepath.Join(t.TempDir(), "nope.csv"), westCoast, 1, 1); err == nil ||
		!strings.Contains(err.Error(), "nope.csv") || !strings.Contains(err.Error(), "fetch-ais") {
		t.Fatalf("a missing file must name the path and suggest fetch-ais: %v", err)
	}
	if _, err := LoadAIS(writeCSV(t, "MMSI,LAT,LON\n1,2,3\n"), westCoast, 1, 1); err == nil ||
		!strings.Contains(err.Error(), "basedatetime") || !strings.Contains(err.Error(), "MMSI,LAT,LON") {
		t.Fatalf("a missing column must be named with the header seen: %v", err)
	}
	if _, err := LoadAIS(writeCSV(t, legacyHeader+legacyRows), model.BBox{MinLon: 0, MinLat: 0, MaxLon: 1, MaxLat: 1}, 1, 1); err == nil ||
		!strings.Contains(err.Error(), "no tracks in bbox") {
		t.Fatalf("an empty box must be an error: %v", err)
	}
	if _, err := LoadAIS(writeCSV(t, ""), westCoast, 1, 1); err == nil {
		t.Fatal("an empty file must be an error")
	}
}
