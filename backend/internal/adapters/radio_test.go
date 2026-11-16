package adapters

import (
	"github.com/np1-control-center/ncc/internal/trips"
	"testing"
	"time"
)

func TestRadioUsesFreshLiveCoordinateWithDelayedNMEAWithoutBorrowingOldAccuracy(t *testing.T) {
	at := time.Date(2026, 11, 14, 3, 0, 0, 0, time.UTC)
	fix := &trips.Sample{At: at.Add(-3 * time.Minute), Lat: 35, Lon: 139, AccuracyM: floatRadio(5)}
	meta := &trips.MetaPosition{At: at, Lat: 35.0001, Lon: 139.0001}
	p := radioLivePosition(combineGNSS(at, meta, false, fix, nil), at)
	if p == nil || p.At != at || p.Lat != meta.Lat || p.Source != "stock-image-recognition" || p.AccuracyM != nil {
		t.Fatal(p)
	}
	if p = radioLivePosition(combineGNSS(at, meta, false, nil, nil), at); p != nil {
		t.Fatal("unconfirmed last-known position joined to radio", p)
	}
	if p = radioLivePosition(combineGNSS(at, nil, false, fix, nil), at); p != nil {
		t.Fatal("three-minute-old coordinate joined to current radio", p)
	}
	meta.At = at.Add(-6 * time.Second)
	if p = radioLivePosition(combineGNSS(at, meta, true, nil, nil), at); p != nil {
		t.Fatal("timestamp tolerance exceeded", p)
	}
}

func floatRadio(v float64) *float64 { return &v }
