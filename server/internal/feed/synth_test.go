package feed

import (
	"testing"
	"time"
)

func TestSynthDeterministic(t *testing.T) {
	a, b := NewSynthFleet(50, 42), NewSynthFleet(50, 42)
	at := time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)
	for i := 0; i < a.Len(); i++ {
		s1, ok1 := a.SampleAt(i, at)
		s2, ok2 := b.SampleAt(i, at)
		if ok1 != ok2 || s1.String() != s2.String() {
			t.Fatalf("vessel %d differs between two fleets: %v and %v", i, s1, s2)
		}
	}
}

func TestSynthMoves(t *testing.T) {
	f := NewSynthFleet(50, 42)
	at := time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)
	for i := 0; i < f.Len(); i++ {
		s1, _ := f.SampleAt(i, at)
		s2, _ := f.SampleAt(i, at.Add(time.Hour))
		if s1.Lat == s2.Lat && s1.Lon == s2.Lon {
			t.Fatalf("vessel %d did not move in an hour", i)
		}
	}
}

func TestSynthStaysOnTheCoast(t *testing.T) {
	f := NewSynthFleet(2000, 42)
	at := time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)
	for hour := 0; hour < 48; hour += 3 {
		for i := 0; i < f.Len(); i++ {
			s, ok := f.SampleAt(i, at.Add(time.Duration(hour)*time.Hour))
			if !ok {
				t.Fatalf("vessel %d has no sample", i)
			}
			if s.Lat < 30 || s.Lat > 50 || s.Lon < -128 || s.Lon > -116 {
				t.Fatalf("vessel %d at %v, %v", i, s.Lat, s.Lon)
			}
		}
	}
}
