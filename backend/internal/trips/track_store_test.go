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
