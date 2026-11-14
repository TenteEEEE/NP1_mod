package adapters

import (
	"testing"
	"time"

	"github.com/np1-control-center/ncc/internal/trips"
)

func TestPlateMetaNearPicksTheDetectionSecond(t *testing.T) {
	base := time.Date(2026, 11, 14, 14, 0, 0, 0, time.UTC)
	var tr metaTracker
	for s := 0; s < 60; s++ {
		// About 15 m/s north.
		tr.history = append(tr.history, trips.MetaPosition{At: base.Add(time.Duration(s) * time.Second), Lat: 35.5 + float64(s)*0.000135, Lon: 139.4})
	}
	p, moving := tr.near(base.Add(20*time.Second+300*time.Millisecond), plateMetaTolerance)
	if p == nil || !p.At.Equal(base.Add(20*time.Second)) || !moving {
		t.Fatalf("near = %+v moving %v", p, moving)
	}
	if p, _ := tr.near(base.Add(70*time.Second), plateMetaTolerance); p != nil {
		t.Fatalf("a record beyond the tolerance was used: %+v", p)
	}
}

func TestPlateMetaTrustNeedsMovementOrANearbyFix(t *testing.T) {
	at := time.Date(2026, 11, 14, 14, 0, 0, 0, time.UTC)
	meta := trips.MetaPosition{At: at, Lat: 35.5, Lon: 139.4}
	if metaTrusted(at, meta, false, nil) {
		t.Fatal("a stationary record without any fix was trusted")
	}
	near := &trips.Sample{At: at.Add(-2 * time.Minute), Lat: 35.501, Lon: 139.4}
	if !metaTrusted(at, meta, false, near) {
		t.Fatal("a stationary record next to a recent fix was not trusted")
	}
	far := &trips.Sample{At: at.Add(-10 * time.Second), Lat: 36.5, Lon: 139.4}
	if metaTrusted(at, meta, false, far) {
		t.Fatal("a stationary record far from the newest fix was trusted")
	}
	if !metaTrusted(at, meta, true, nil) {
		t.Fatal("a moving record was not trusted")
	}
}
