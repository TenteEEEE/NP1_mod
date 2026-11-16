package trips

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRetainedTrackSurvivesSourceDeletionRestartAndPreservesBreaks(t *testing.T) {
	dir := t.TempDir()
	store := NewTrackStore(dir)
	at := time.Date(2026, 11, 13, 1, 0, 0, 0, time.UTC)
	points := []TrackPoint{{At: at, Lat: 35, Lon: 139, Segment: 0}, {At: at.Add(time.Second), Lat: 35, Lon: 139.0001, Segment: 0}, {At: at.Add(2 * time.Second), Lat: 35, Lon: 139.0002, Segment: 1}}
	report := TrackReport{Date: "2026-11-13", Source: "stock-recording-nmea", Points: points, SourceTruncated: true}
	added, err := store.Ingest(report, at.Add(time.Hour))
	if err != nil || added != 3 {
		t.Fatal(added, err)
	}
	filename := filepath.Join(dir, "journey-tracks", "2026-11-13.json.gz")
	before, _ := os.Stat(filename)
	added, err = store.Ingest(report, at.Add(2*time.Hour))
	after, _ := os.Stat(filename)
	if err != nil || added != 0 || !before.ModTime().Equal(after.ModTime()) {
		t.Fatal("not idempotent", added, err)
	}
	restored, err := NewTrackStore(dir).Read("2026-11-13")
	if err != nil || len(restored.Points) != 3 || len(restored.Segments) != 2 || !restored.SourceTruncated {
		t.Fatal(restored, err)
	}
	if _, err = store.Read("../bad"); err == nil {
		t.Fatal("invalid date accepted")
	}
}

func TestReadRetainedTrackDoesNotCreateStorage(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "absent")
	report, err := NewTrackStore(dir).Read("2026-11-14")
	if err != nil || len(report.Points) != 0 {
		t.Fatal(report, err)
	}
	if _, err = os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("read created files")
	}
}

func TestRestoringDetailedTrackKeepsEveryPointAndLineAfterPartialSDScans(t *testing.T) {
	state := t.TempDir()
	store := NewTrackStore(state)
	at := time.Date(2026, 11, 15, 1, 0, 0, 0, time.UTC)
	full := TrackReport{Date: "2026-11-15", Source: "stock-recording-nmea"}
	for i := 0; i < 6724; i++ {
		full.Points = append(full.Points, TrackPoint{At: at.Add(time.Duration(i) * time.Second), Lat: 35, Lon: 139 + float64(i)*.00001, Segment: 0})
	}
	partial := full
	partial.Points = append([]TrackPoint(nil), full.Points[3819:]...)
	if _, err := store.Ingest(partial, at.Add(3*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Ingest(full, at.Add(3*time.Hour)); err != nil {
		t.Fatal(err)
	}
	// A subsequent scan of the remaining SD files cannot replace the complete
	// recovered track with just its shorter tail.
	if _, err := store.Ingest(partial, at.Add(3*time.Hour)); err != nil {
		t.Fatal(err)
	}
	got, err := NewTrackStore(state).Read(full.Date)
	if err != nil || len(got.Points) != len(full.Points) || len(got.Segments) != 1 || got.Segments[0].PointCount != len(full.Points) {
		t.Fatalf("detailed route lost points or line continuity: points=%d segments=%d error=%v", len(got.Points), len(got.Segments), err)
	}
	for i, point := range full.Points {
		if point != got.Points[i] {
			t.Fatalf("measured point %d changed", i)
		}
	}
}
