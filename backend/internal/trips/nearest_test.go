package trips

import (
	"testing"
	"time"
)

func TestNearestSamplesWithinTolerance(t *testing.T) {
	base := time.Date(2026, 11, 14, 14, 0, 0, 0, time.UTC)
	fix := func(s int, lat float64) Sample {
		return Sample{At: base.Add(time.Duration(s) * time.Second), Lat: lat, Lon: 139, Valid: true, Quality: 1}
	}
	samples := []Sample{fix(10, 35.3), fix(0, 35.1), fix(1, 35.2), {At: base.Add(5 * time.Second), Lat: 35.9, Lon: 139}}
	got := NearestSamples(samples, []time.Time{base.Add(1400 * time.Millisecond), base.Add(5 * time.Second), base.Add(30 * time.Second)}, 3*time.Second)
	if got[0] == nil || got[0].Lat != 35.2 {
		t.Fatalf("nearest to 1.4 s = %+v", got[0])
	}
	if got[1] != nil {
		t.Fatalf("an invalid fix or one beyond tolerance was used: %+v", got[1])
	}
	if got[2] != nil {
		t.Fatalf("no sample within tolerance expected: %+v", got[2])
	}
}
