package adapters

import (
	"context"
	"time"

	"github.com/np1-control-center/ncc/internal/trips"
)

// Plate detections need the position at the moment the frame was taken. The recorder's NMEA
// sidecars appear only when a clip closes (about every three minutes), long after the sensor
// has to publish a detection, so the plate sensor follows the stock per-second record the
// dashboard uses and looks up the second nearest to each detection. The same rule as the
// dashboard decides whether that record is a real position: it must be moving or agree with
// an NMEA fix from the last ten minutes. Records that still get no position here are filled
// in later from the recorder's GNSS log (see HandleRecordingPositions).

// plateMetaTolerance is how far the nearest per-second record may be from the detection.
const plateMetaTolerance = 2 * time.Second

// TrackLivePosition keeps the per-second history current so a detection can be matched to
// the record of its own second. It runs in the plate sensor process.
func TrackLivePosition(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		liveMeta.read(time.Now())
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// near returns the history record closest to at (within tol) and whether the position was
// moving up to that record.
func (t *metaTracker) near(at time.Time, tol time.Duration) (*trips.MetaPosition, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	best := -1
	for i, h := range t.history {
		d := absDuration(h.At.Sub(at))
		if d <= tol && (best < 0 || d < absDuration(t.history[best].At.Sub(at))) {
			best = i
		}
	}
	if best < 0 {
		return nil, false
	}
	p := t.history[best]
	return &p, metaMoving(t.history[:best+1], p.At)
}

func absDuration(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
}

// metaTrusted reports whether a per-second record is a real position rather than a stale
// last-known one: it is moving, or it is near the newest NMEA fix (within 500 m plus 50 m/s
// since that fix).
func metaTrusted(at time.Time, meta trips.MetaPosition, moving bool, fix *trips.Sample) bool {
	if moving {
		return true
	}
	if fix == nil {
		return false
	}
	limit := 500 + 50*at.Sub(fix.At).Seconds()
	return distanceM(UplinkPosition{Lat: meta.Lat, Lon: meta.Lon}, UplinkPosition{Lat: fix.Lat, Lon: fix.Lon}) <= limit
}

// PlatePosition returns the position at a detection time, or nil when none is known yet.
func PlatePosition(ctx context.Context, at time.Time) *trips.Sample {
	liveMeta.read(time.Now())
	fix, e := liveGNSS.LatestWithin(ctx, at, gnssRecentWindow)
	if e != nil {
		fix = nil
	}
	if meta, moving := liveMeta.near(at, plateMetaTolerance); meta != nil && metaTrusted(meta.At, *meta, moving, fix) {
		return &trips.Sample{At: meta.At, Lat: meta.Lat, Lon: meta.Lon, Source: "stock-image-recognition"}
	}
	if fix != nil && absDuration(at.Sub(fix.At)) <= 5*time.Second {
		return fix
	}
	return nil
}
