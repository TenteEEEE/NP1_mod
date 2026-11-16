package numbershare

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/np1-control-center/ncc/internal/plate"
)

func testRecord(at time.Time) plate.Record {
	accuracy := 8.5
	return plate.Record{ID: "record-1", FirstSeen: at, Reading: plate.Reading{Region: "品川", Class: "300", Kana: "あ", Number: "1234", Confidence: .9}, Location: &plate.Location{At: at, Lat: 35.6, Lon: 139.7, AccuracyM: &accuracy}}
}

func configureTest(t *testing.T, s *Service, endpoint string) {
	t.Helper()
	key := "fictional-test-key"
	got, err := s.Configure(1, true, endpoint, &key)
	if err != nil || !got.Enabled || !got.KeyConfigured {
		t.Fatalf("configure = %+v, %v", got, err)
	}
	b, err := os.ReadFile(filepath.Join(s.dir, "config.json"))
	if err != nil || !json.Valid(b) {
		t.Fatalf("config file: %v", err)
	}
	if _, ok := any(got).(interface{ APIKey() string }); ok {
		t.Fatal("public config unexpectedly exposes a key")
	}
}

func TestQueueRetriesSameImmutableEventAndPersists(t *testing.T) {
	var mu sync.Mutex
	requests := [][]byte{}
	keys := []string{}
	calls := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		var raw json.RawMessage
		_ = json.NewDecoder(r.Body).Decode(&raw)
		mu.Lock()
		requests = append(requests, append([]byte(nil), raw...))
		keys = append(keys, r.Header.Get("X-API-Key"))
		calls++
		call := calls
		mu.Unlock()
		if r.URL.Path != "/api/sightings" {
			http.NotFound(w, r)
			return
		}
		if call == 1 {
			http.Error(w, `{"error":"temporary"}`, http.StatusServiceUnavailable)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"saved": true, "match": map[string]string{"status": "exact"}})
	}))
	defer server.Close()

	now := time.Date(2026, 11, 16, 6, 0, 0, 0, time.UTC)
	s := New(t.TempDir(), server.Client())
	s.clock = func() time.Time { return now }
	configureTest(t, s, server.URL+"/api/")
	if err := s.Enqueue(testRecord(now)); err != nil {
		t.Fatal(err)
	}
	// A richer frame before the first network attempt updates the durable body.
	richer := testRecord(now)
	richer.Reading.Class = "301"
	if err := s.Enqueue(richer); err != nil {
		t.Fatal(err)
	}
	item, cfg, ok := s.nextDue()
	if !ok {
		t.Fatal("queue not due")
	}
	status, body, err := s.send(context.Background(), cfg, item)
	s.complete(item, status, body, err)
	first := s.Status()
	if first.Pending != 1 || first.Items[0].Attempts != 1 || first.Items[0].NextAttempt == nil {
		t.Fatalf("after failure = %+v", first)
	}
	now = now.Add(6 * time.Second)
	item, cfg, ok = s.nextDue()
	if !ok {
		t.Fatal("retry not due")
	}
	status, body, err = s.send(context.Background(), cfg, item)
	s.complete(item, status, body, err)
	final := s.Status()
	if final.Sent != 1 || final.Pending != 0 || final.Items[0].MatchStatus != "exact" {
		t.Fatalf("after success = %+v", final)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(requests) != 2 || string(requests[0]) != string(requests[1]) {
		t.Fatalf("retry changed payload: %q / %q", requests[0], requests[1])
	}
	if keys[0] != "fictional-test-key" || keys[1] != keys[0] {
		t.Fatalf("keys = %q", keys)
	}
	var sent SightingPayload
	if json.Unmarshal(requests[0], &sent) != nil || sent.EventID == "" || sent.Plate.ClassificationNumber != "301" {
		t.Fatalf("payload = %+v", sent)
	}
	reloaded := New(filepath.Dir(s.dir), server.Client()).Status()
	if reloaded.Sent != 1 || reloaded.Items[0].EventID != sent.EventID {
		t.Fatalf("reloaded = %+v", reloaded)
	}
}

func TestPermanentErrorBlocksUntilManualRetry(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"error":"bad plate"}`))
	}))
	defer server.Close()
	now := time.Now().UTC()
	s := New(t.TempDir(), server.Client())
	s.clock = func() time.Time { return now }
	configureTest(t, s, server.URL+"/")
	if err := s.Enqueue(testRecord(now)); err != nil {
		t.Fatal(err)
	}
	item, cfg, _ := s.nextDue()
	status, body, err := s.send(context.Background(), cfg, item)
	s.complete(item, status, body, err)
	result := s.Status()
	if result.Blocked != 1 || result.Items[0].HTTPStatus != 422 || result.Items[0].NextAttempt != nil {
		t.Fatalf("blocked = %+v", result)
	}
	if err := s.Retry(item.EventID); err != nil {
		t.Fatal(err)
	}
	if result = s.Status(); result.Pending != 1 || result.Blocked != 0 {
		t.Fatalf("manual retry = %+v", result)
	}
}

func TestEstimatedLocationIsNotShared(t *testing.T) {
	now := time.Now()
	record := testRecord(now)
	record.Location.Estimated = true
	p := payloadFor(record, "event")
	if p.Location != nil {
		t.Fatalf("estimated location leaked as measured: %+v", p.Location)
	}
}

func TestSharedMarksLocalEventsAndFiltersOthers(t *testing.T) {
	var localEvent string
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/sightings" {
			_ = json.NewEncoder(w).Encode(map[string]any{"items": []map[string]any{
				{"id": 1, "event_id": localEvent, "observed_at": "2026-11-16T06:00:00Z", "received_at": "2026-11-16T06:00:01Z", "match_status": "exact", "plate": map[string]string{"serial_number": "12-34", "region": "品川"}, "location": map[string]any{"latitude": 35.6, "longitude": 139.7}},
				{"id": 2, "event_id": "00000000-0000-4000-8000-000000000002", "observed_at": "2026-11-16T06:01:00Z", "received_at": "2026-11-16T06:01:01Z", "match_status": "none", "plate": map[string]string{"serial_number": "56-78", "region": "練馬"}},
			}, "next_cursor": nil})
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()
	s := New(t.TempDir(), server.Client())
	configureTest(t, s, server.URL+"/api/")
	if err := s.Enqueue(testRecord(time.Now())); err != nil {
		t.Fatal(err)
	}
	localEvent = s.Status().Items[0].EventID
	result, err := s.Shared(context.Background(), SharedQuery{Scope: "others", Text: "練馬"})
	if err != nil || len(result.Items) != 1 || result.Items[0].Mine || result.Items[0].Plate.SerialNumber != "56-78" {
		t.Fatalf("shared = %+v, %v", result, err)
	}
}

func TestConfigureRejectsPlainHTTPAndRequiresCredentials(t *testing.T) {
	s := New(t.TempDir(), nil)
	key := "x"
	if _, err := s.Configure(1, true, "http://example.test/api", &key); err == nil {
		t.Fatal("plain HTTP accepted")
	}
	if _, err := s.Configure(1, true, "https://example.test/api", nil); err == nil {
		t.Fatal("enabled without key accepted")
	}
}

func TestManualRangeUploadRunsWithRealtimeDisabledAndDeduplicates(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"saved": true})
	}))
	defer server.Close()
	s := New(t.TempDir(), server.Client())
	configureTest(t, s, server.URL+"/")
	if _, err := s.Configure(2, false, server.URL+"/", nil); err != nil {
		t.Fatal(err)
	}
	record := testRecord(time.Now())
	result, err := s.EnqueueUpload([]plate.Record{record, record})
	if err != nil || result.Total != 2 || result.Queued != 1 || result.Existing != 1 {
		t.Fatalf("upload = %+v, %v", result, err)
	}
	item, _, due := s.nextDue()
	if !due || !item.Manual || item.RecordID != record.ID {
		t.Fatalf("manual item is not due while realtime is disabled: %+v %v", item, due)
	}
	if result, err = s.EnqueueUpload([]plate.Record{record}); err != nil || result.Queued != 0 || result.Existing != 1 {
		t.Fatalf("repeat upload = %+v, %v", result, err)
	}
}

func TestSharedFetchesEveryCursorPage(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		page := 0
		if cursor := r.URL.Query().Get("cursor"); cursor != "" {
			page, _ = strconv.Atoi(strings.TrimPrefix(cursor, "page-"))
		}
		var next *string
		if page < 6 {
			value := "page-" + strconv.Itoa(page+1)
			next = &value
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"items": []map[string]any{{
			"id": page + 1, "event_id": "event-" + strconv.Itoa(page), "observed_at": "2026-11-16T06:00:00Z",
			"received_at": "2026-11-16T06:00:01Z", "match_status": "none", "plate": map[string]string{"serial_number": "12-34"},
		}}, "next_cursor": next})
	}))
	defer server.Close()
	s := New(t.TempDir(), server.Client())
	configureTest(t, s, server.URL+"/")
	result, err := s.Shared(context.Background(), SharedQuery{})
	if err != nil || len(result.Items) != 7 || result.Truncated {
		t.Fatalf("all pages = %d truncated=%v err=%v", len(result.Items), result.Truncated, err)
	}
}
