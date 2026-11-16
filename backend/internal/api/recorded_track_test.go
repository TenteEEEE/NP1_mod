package api

import (
	"encoding/json"
	"github.com/np1-control-center/ncc/internal/trips"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRecordedTrackShowsRetainedPastDaysWhenSDSourceIsGone(t *testing.T) {
	store := trips.NewTrackStore(t.TempDir())
	at := time.Date(2026, 11, 13, 1, 0, 0, 0, time.UTC)
	_, err := store.Ingest(trips.TrackReport{Date: "2026-11-13", Source: "stock-recording-nmea", Points: []trips.TrackPoint{{At: at, Lat: 35, Lon: 139}, {At: at.Add(time.Second), Lat: 35, Lon: 139.0001}}}, at.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{tracks: store, client: &http.Client{Transport: journeyRoundTrip(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 503, Body: io.NopCloser(strings.NewReader("{}"))}, nil
	})}}
	w := httptest.NewRecorder()
	s.recordedTrack(w, httptest.NewRequest("GET", "/api/v1/recordings/track?date=2026-11-13", nil))
	var out struct {
		Data trips.TrackReport `json:"data"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &out) != nil || len(out.Data.Points) != 2 || out.Data.Source != "ncc-retained-measured-positions" {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestDisplayedDashboardTrackSurvivesSDOverwriteAndServerRestart(t *testing.T) {
	state := t.TempDir()
	// The source covers both sides of JST midnight within a rolling 24 hours.
	at := time.Date(2026, 11, 13, 14, 58, 0, 0, time.UTC)
	points := make([]trips.TrackPoint, 360)
	for i := range points {
		segment := 0
		if i >= 180 {
			segment = 1 // preserve a real break, despite the one-second interval
		}
		points[i] = trips.TrackPoint{At: at.Add(time.Duration(i) * time.Second), Lat: 35, Lon: 139 + float64(i)*.00001, Segment: segment}
	}
	reference := trips.TrackReport{Points: points, Source: "stock-recording-nmea"}
	remaining := true
	client := &http.Client{Transport: journeyRoundTrip(func(r *http.Request) (*http.Response, error) {
		track := trips.TrackReport{Points: []trips.TrackPoint{}, Segments: []trips.TrackSegment{}}
		if remaining {
			track = reference
		}
		body, _ := json.Marshal(map[string]any{"status": "available", "data": track})
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(body)))}, nil
	})}
	target := "/api/v1/recordings/track?from=2026-11-13T06:00:00Z&to=2026-11-14T06:00:00Z"
	read := func(s *Server) trips.TrackReport {
		w := httptest.NewRecorder()
		s.recordedTrack(w, httptest.NewRequest("GET", target, nil))
		var out struct {
			Data trips.TrackReport `json:"data"`
		}
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &out) != nil {
			t.Fatalf("route read failed: %d %s", w.Code, w.Body.String())
		}
		return out.Data
	}
	first := read(&Server{tracks: trips.NewTrackStore(state), client: client})
	remaining = false
	// No collector runs in this fixture: the actual displayed response must
	// have retained the full track before the original SD data disappeared.
	restored := read(&Server{tracks: trips.NewTrackStore(state), client: client})
	if len(first.Points) != len(points) || len(restored.Points) != len(points) {
		t.Fatalf("displayed GPS points lost: first=%d after=%d expected=%d", len(first.Points), len(restored.Points), len(points))
	}
	for i, p := range points {
		got := restored.Points[i]
		if !got.At.Equal(p.At) || got.Lat != p.Lat || got.Lon != p.Lon || got.Segment != first.Points[i].Segment {
			t.Fatalf("displayed point or line %d changed", i)
		}
	}
	if restored.Points[119].Segment != restored.Points[120].Segment {
		t.Fatal("midnight split a displayed continuous line")
	}
	if restored.Points[179].Segment == restored.Points[180].Segment {
		t.Fatal("a real GPS break was joined")
	}
}

func TestDisplayedTrackCannotSilentlySkipRetentionFailure(t *testing.T) {
	state := t.TempDir()
	directory := filepath.Join(state, "journey-tracks")
	if err := os.MkdirAll(directory, 0700); err != nil {
		t.Fatal(err)
	}
	file, err := os.Create(filepath.Join(directory, "full-fixture"))
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate(128*1024*1024 + 1); err != nil {
		t.Fatal(err)
	}
	file.Close()
	client := &http.Client{Transport: journeyRoundTrip(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"data":{"points":[{"at":"2026-11-13T01:00:00Z","lat":35,"lon":139,"segment":0}]}}`))}, nil
	})}
	s := &Server{tracks: trips.NewTrackStore(state), client: client}
	w := httptest.NewRecorder()
	s.recordedTrack(w, httptest.NewRequest("GET", "/api/v1/recordings/track?date=2026-11-13", nil))
	if w.Code != 503 || !strings.Contains(w.Body.String(), "TRACK_RETENTION_UNAVAILABLE") || s.logger.TrackRetentionError == "" {
		t.Fatalf("display succeeded without retaining its GPS: %d %s", w.Code, w.Body.String())
	}
}
