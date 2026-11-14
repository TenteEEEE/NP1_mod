package api

import (
	"encoding/json"
	"github.com/np1-control-center/ncc/internal/trips"
	"io"
	"net/http"
	"net/http/httptest"
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
