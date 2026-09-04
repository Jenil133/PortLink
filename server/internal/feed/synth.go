package feed

import (
	"fmt"
	"math"
	"math/rand"
	"time"

	lensv1 "github.com/portlink/lens/server/internal/gen/lens/v1"
)

// Waypoints are {lat, lon}. Routes stay offshore so vessels are (almost always) drawn on water.
var routeWaypoints = [][][2]float64{
	{ // San Francisco -> Santa Barbara Channel -> Los Angeles
		{37.75, -122.70}, {36.60, -122.20}, {35.30, -121.30}, {34.40, -120.70},
		{34.20, -119.80}, {34.05, -119.20}, {33.70, -118.40},
	},
	{ // Los Angeles -> San Diego
		{33.70, -118.25}, {33.30, -117.80}, {32.65, -117.30},
	},
	{ // San Francisco -> Cape Flattery
		{37.80, -122.75}, {38.90, -123.90}, {40.40, -124.60}, {42.00, -124.70},
		{44.00, -124.50}, {46.20, -124.40}, {48.40, -124.90},
	},
	{ // San Francisco -> open Pacific
		{37.78, -122.60}, {37.60, -123.50}, {37.00, -126.00},
	},
}

type route struct {
	pts   [][2]float64
	cum   []float64 // cumulative metres at each waypoint
	total float64
}

type synthVessel struct {
	mmsi           uint32
	name           string
	vtype          uint32
	rt             *route
	speedMps       float64
	phaseM         float64 // starting offset along the route
	offLat, offLon float64 // constant lateral offset so vessels do not stack on one line
}

type SynthFleet struct{ vessels []synthVessel }

func NewSynthFleet(n int, seed int64) *SynthFleet {
	routes := make([]*route, len(routeWaypoints))
	for i, pts := range routeWaypoints {
		r := &route{pts: pts, cum: make([]float64, len(pts))}
		for j := 1; j < len(pts); j++ {
			r.cum[j] = r.cum[j-1] + haversineM(pts[j-1][0], pts[j-1][1], pts[j][0], pts[j][1])
		}
		r.total = r.cum[len(pts)-1]
		routes[i] = r
	}
	rng := rand.New(rand.NewSource(seed)) // fixed seed: same fleet on every run
	types := []uint32{70, 80, 30, 60}     // cargo, tanker, fishing, passenger
	f := &SynthFleet{vessels: make([]synthVessel, n)}
	for i := range f.vessels {
		rt := routes[rng.Intn(len(routes))]
		f.vessels[i] = synthVessel{
			mmsi:     900000000 + uint32(i),
			name:     fmt.Sprintf("SYN-%04d", i),
			vtype:    types[i%len(types)],
			rt:       rt,
			speedMps: (8 + rng.Float64()*12) * 0.514444, // 8 to 20 knots
			phaseM:   rng.Float64() * 2 * rt.total,
			offLat:   (rng.Float64() - 0.5) * 0.04,
			offLon:   (rng.Float64() - 0.5) * 0.04,
		}
	}
	return f
}

func (f *SynthFleet) Len() int { return len(f.vessels) }

func (f *SynthFleet) SampleAt(i int, t time.Time) (*lensv1.VesselSample, bool) {
	v := &f.vessels[i]
	// Distance travelled is linear in unix time, then folded into a ping-pong along the route.
	d := math.Mod(v.phaseM+v.speedMps*float64(t.UnixMilli())/1000, 2*v.rt.total)
	reversed := d > v.rt.total
	if reversed {
		d = 2*v.rt.total - d
	}
	// Find the segment containing distance d.
	j := 1
	for j < len(v.rt.cum)-1 && v.rt.cum[j] < d {
		j++
	}
	a, b := v.rt.pts[j-1], v.rt.pts[j]
	k := (d - v.rt.cum[j-1]) / (v.rt.cum[j] - v.rt.cum[j-1])
	lat := a[0] + (b[0]-a[0])*k + v.offLat
	lon := a[1] + (b[1]-a[1])*k + v.offLon
	cog := bearingDeg(a[0], a[1], b[0], b[1])
	if reversed {
		cog = math.Mod(cog+180, 360)
	}
	return &lensv1.VesselSample{
		Mmsi: v.mmsi, TsMs: t.UnixMilli(), Lat: lat, Lon: lon,
		SogKn: float32(v.speedMps / 0.514444), CogDeg: float32(cog), HeadingDeg: float32(cog),
		Name: v.name, VesselType: v.vtype,
	}, true
}

func haversineM(lat1, lon1, lat2, lon2 float64) float64 {
	const R = 6371000.0
	p1, p2 := lat1*math.Pi/180, lat2*math.Pi/180
	dp, dl := p2-p1, (lon2-lon1)*math.Pi/180
	h := math.Sin(dp/2)*math.Sin(dp/2) + math.Cos(p1)*math.Cos(p2)*math.Sin(dl/2)*math.Sin(dl/2)
	return 2 * R * math.Asin(math.Sqrt(h))
}

func bearingDeg(lat1, lon1, lat2, lon2 float64) float64 {
	p1, p2 := lat1*math.Pi/180, lat2*math.Pi/180
	dl := (lon2 - lon1) * math.Pi / 180
	y := math.Sin(dl) * math.Cos(p2)
	x := math.Cos(p1)*math.Sin(p2) - math.Sin(p1)*math.Cos(p2)*math.Cos(dl)
	return math.Mod(math.Atan2(y, x)*180/math.Pi+360, 360)
}
