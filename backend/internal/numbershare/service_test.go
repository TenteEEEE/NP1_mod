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

	"github.com/np1-control-center/ncc/internal/config"
	"github.com/np1-control-center/ncc/internal/plate"
)

func testRecord(at time.Time) plate.Record {
	accuracy := 8.5
	return plate.Record{ID: "record-1", FirstSeen: at, Reading: plate.Reading{Region: "品川", Class: "300", Kana: "あ", Number: "1234", Confidence: .9}, Location: &plate.Location{At: at, Lat: 35.6, Lon: 139.7, AccuracyM: &accuracy}}
}

func configureTest(t *testing.T, s *Service, endpoint string) {
	t.Helper()
	s.settle = 0
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
		_ = json.NewEncoder(w).Encode(map[string]any{"saved": true, "match": map[string]any{"status": "exact", "plate": map[string]any{"category": "危険", "comment": "統合結果", "metadata_present": true}}})
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
	visible, ok := Results(filepath.Dir(s.dir))["record-1"]
	if !ok || visible.Status != "exact" || visible.Category != "危険" || visible.Comment != "統合結果" {
		t.Fatalf("saved match result = %+v, %v", visible, ok)
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
	if json.Unmarshal(requests[0], &sent) != nil || sent.EventID != eventID("record-1") || sent.Plate.SerialNumber != "12-34" || sent.Plate.ClassificationNumber != "301" {
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

func TestRealtimeQueueWaitsForMergedRecordToSettle(t *testing.T) {
	now := time.Date(2026, 11, 17, 3, 0, 0, 0, time.UTC)
	s := New(t.TempDir(), nil)
	s.clock = func() time.Time { return now }
	key := "fictional-test-key"
	if _, err := s.Configure(1, true, "https://share.example/api/", &key); err != nil {
		t.Fatal(err)
	}
	record := testRecord(now)
	record.LastSeen = now
	if err := s.Enqueue(record); err != nil {
		t.Fatal(err)
	}
	if _, _, due := s.nextDue(); due {
		t.Fatal("record was sent before its merge window settled")
	}
	now = now.Add(defaultGrace)
	if _, _, due := s.nextDue(); !due {
		t.Fatal("settled record did not become due")
	}
}

func TestEstimatedLocationIsNotShared(t *testing.T) {
	now := time.Now()
	record := testRecord(now)
	record.Location.Estimated = true
	p, ok := payloadFor(record, "event")
	if !ok {
		t.Fatal("valid record was rejected")
	}
	if p.Location != nil {
		t.Fatalf("estimated location leaked as measured: %+v", p.Location)
	}
}

func TestPayloadUsesNumberShareMillisecondTimestamp(t *testing.T) {
	at := time.Date(2026, 11, 16, 6, 0, 0, 817821101, time.UTC)
	payload, ok := payloadFor(testRecord(at), "event")
	if !ok {
		t.Fatal("valid record was rejected")
	}
	if payload.ObservedAt != "2026-11-16T06:00:00.817Z" {
		t.Fatalf("observed_at = %q", payload.ObservedAt)
	}
}

func TestReloadMigratesRejectedNanosecondTimestampWithoutChangingEventID(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "numbershare")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	item := QueueItem{
		EventID: "11111111-1111-4111-8111-111111111111", RecordID: "record-1", State: "blocked",
		HTTPStatus: 422, LastError: "invalid observed_at", QueuedAt: now, Manual: true,
		Payload: SightingPayload{EventID: "11111111-1111-4111-8111-111111111111", ObservedAt: "2026-11-16T06:00:00.817821101Z", Plate: Plate{SerialNumber: "1234"}},
	}
	b, _ := json.Marshal([]QueueItem{item})
	if err := os.WriteFile(filepath.Join(dir, "queue.json"), b, 0600); err != nil {
		t.Fatal(err)
	}
	status := New(root, nil).Status()
	got := status.Items[0]
	if status.Blocked != 1 || got.EventID != item.EventID || got.Payload.ObservedAt != "2026-11-16T06:00:00.817Z" || got.HTTPStatus != 422 {
		t.Fatalf("migrated = %+v", status)
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

func TestLegacyConfigMigratesOnceIntoManagedAPISettings(t *testing.T) {
	root := t.TempDir()
	legacyPath := filepath.Join(root, "private", "numbershare.json")
	writeJSON(t, legacyPath, Config{Enabled: true, BaseURL: "https://share.example/api/", APIKey: "legacy-device-key-0123456789", CutoverAt: time.Now()})
	writeJSON(t, filepath.Join(root, "private", "numbershare-recovery.json"), map[string]string{"userHandle": "legacy_owner", "recoveryCode": "legacy-recovery-secret"})
	migrated, err := MigrateLegacyConfig(legacyPath, filepath.Join(root, "state"))
	if err != nil || !migrated {
		t.Fatalf("first migration = %v, %v", migrated, err)
	}
	managed, err := loadAPIConfig(ManagedConfigPath(filepath.Join(root, "state")))
	if err != nil || !managed.Enabled || managed.Endpoint != "https://share.example/api/" || managed.APIKey != "legacy-device-key-0123456789" || managed.UserHandle != "legacy_owner" || managed.RecoveryCode != "legacy-recovery-secret" {
		t.Fatalf("managed config = %+v, %v", managed, err)
	}
	status := New(filepath.Join(root, "state"), nil).Status()
	if !status.Config.KeyConfigured || status.Config.UserHandle != "legacy_owner" {
		t.Fatalf("migrated main config was not treated as initialized: %+v", status.Config)
	}
	managed.Endpoint = "https://owner.example/api/"
	if err = config.Atomic(ManagedConfigPath(filepath.Join(root, "state")), managed); err != nil {
		t.Fatal(err)
	}
	if migrated, err = MigrateLegacyConfig(legacyPath, filepath.Join(root, "state")); err != nil || migrated {
		t.Fatalf("second migration = %v, %v", migrated, err)
	}
	kept, _ := loadAPIConfig(ManagedConfigPath(filepath.Join(root, "state")))
	if kept.Endpoint != "https://owner.example/api/" {
		t.Fatalf("owner setting was overwritten: %+v", kept)
	}
}

func TestUIRegistrationStoresIssuedDeviceAndManagementKeys(t *testing.T) {
	const bootstrap = "bootstrap-key-0123456789abcdef"
	const admin = "admin-key-0123456789abcdef"
	const issued = "issued-device-key-0123456789abcdef"
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.Header.Get("X-API-Key")
		switch r.URL.Path {
		case "/api/openapi.json":
			if key != admin {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			_, _ = w.Write([]byte(`{}`))
		case "/api/users":
			if key != bootstrap {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"user_id":"u1","user_handle":"owner_01","device_id":"d1","api_key":"` + issued + `","recovery_code":"recovery-secret"}`))
		case "/api/users/me":
			if key != issued {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			_, _ = w.Write([]byte(`{"user_id":"u1","user_handle":"owner_01","device_id":"d1"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	s := New(t.TempDir(), server.Client())
	result, err := s.Register(context.Background(), RegistrationRequest{
		ExpectedRevision: 1, Endpoint: server.URL + "/api/", UserHandle: "owner_01", DeviceName: "NP1 test",
		BootstrapKey: bootstrap, AdminAPIKey: admin,
	})
	if err != nil || result.UserHandle != "owner_01" || result.DeviceID != "d1" {
		t.Fatalf("register = %+v, %v", result, err)
	}
	status := s.Status()
	if status.Config.Enabled || !status.Config.KeyConfigured || !status.Config.AdminKeyConfigured || status.Config.APIKey != issued || status.Config.AdminAPIKey != admin || status.Config.UserHandle != "owner_01" {
		t.Fatalf("registered config = %+v", status.Config)
	}
	stored, err := loadAPIConfig(s.configPath())
	if err != nil || stored.RecoveryCode != "recovery-secret" || stored.DeviceID != "d1" {
		t.Fatalf("private registration state = %+v, %v", stored, err)
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

func TestManualRangeUploadRequeuesOnlySelectedBlockedRecords(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"error":"invalid observed_at"}`))
	}))
	defer server.Close()
	s := New(t.TempDir(), server.Client())
	configureTest(t, s, server.URL+"/")
	selected := testRecord(time.Date(2026, 11, 14, 12, 0, 0, 987654321, time.FixedZone("JST", 9*60*60)))
	other := testRecord(time.Date(2026, 11, 15, 12, 0, 0, 123456789, time.FixedZone("JST", 9*60*60)))
	other.ID = "record-other-day"
	for _, record := range []plate.Record{selected, other} {
		if err := s.Enqueue(record); err != nil {
			t.Fatal(err)
		}
		item, cfg, due := s.nextDue()
		if !due {
			t.Fatal("queued item was not due")
		}
		status, body, err := s.send(context.Background(), cfg, item)
		s.complete(item, status, body, err)
	}
	before := s.Status()
	if before.Blocked != 2 {
		t.Fatalf("before = %+v", before)
	}
	if _, err := s.Configure(2, false, server.URL+"/", nil); err != nil {
		t.Fatal(err)
	}
	result, err := s.EnqueueUpload([]plate.Record{selected})
	if err != nil || result.Requeued != 1 || result.Queued != 0 || result.Existing != 1 {
		t.Fatalf("upload = %+v, %v", result, err)
	}
	after := s.Status()
	if after.Pending != 1 || after.Blocked != 1 {
		t.Fatalf("after = %+v", after)
	}
	for _, item := range after.Items {
		if item.RecordID == selected.ID && (item.State != "pending" || !item.Manual || item.Payload.ObservedAt != "2026-11-14T12:00:00.987+09:00") {
			t.Fatalf("selected = %+v", item)
		}
		if item.RecordID == other.ID && item.State != "blocked" {
			t.Fatalf("other date changed = %+v", item)
		}
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
