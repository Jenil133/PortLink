package model

// VesselState is the latest known state of one vessel inside the gateway.
type VesselState struct {
	MMSI         uint32
	TsMs         int64
	Lat, Lon     float64
	Sog, Cog     float32 // knots, degrees
	Heading      float32
	Name         string
	VesselType   uint32
	EngineRPM    float32
	FuelRateLph  float32
	EngineTempC  float32
	FuelLevelPct float32
}

type BBox struct {
	MinLon, MinLat, MaxLon, MaxLat float64
}

func (b BBox) Contains(lat, lon float64) bool {
	return lat >= b.MinLat && lat <= b.MaxLat && lon >= b.MinLon && lon <= b.MaxLon
}
