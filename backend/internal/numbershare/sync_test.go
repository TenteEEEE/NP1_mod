package numbershare

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/np1-control-center/ncc/internal/plate"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func writeJSON(t *testing.T, path string, value any) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(value)
	if err := os.WriteFile(path, b, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestSyncOnceSendsStableNewRecordWithoutPrivateFields(t *testing.T) {
	now := time.Date(2026, 11, 16, 1, 0, 0, 0, time.UTC)
	var got map[string]any
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("X-API-Key") != "device-secret" || r.URL.Path != "/api/sightings" {
			t.Fatalf("request auth/path mismatch")
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatal(err)
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"saved":true,"match":{"status":"exact","plate":{"category":"危険","comment":"管理者コメント","metadata_present":true}}}`)), Header: make(http.Header)}, nil
	})}

	dir := t.TempDir()
	conf := filepath.Join(dir, "private", "numbershare.json")
	writeJSON(t, conf, Config{Enabled: true, BaseURL: "https://numbershare.test/api/", APIKey: "device-secret", CutoverAt: now.Add(-time.Hour)})
	record := plate.Record{ID: "pstable-0001", FirstSeen: now.Add(-2 * time.Minute), LastSeen: now.Add(-time.Minute), Reading: plate.Reading{Region: "品川", Class: "300", Kana: "あ", Number: "1234"}, Location: &plate.Location{Lat: 35, Lon: 139}, ImageID: "private-image"}
	writeJSONLine(t, filepath.Join(dir, "state", "plate", "records", plate.Day(record.FirstSeen)+".jsonl"), record)
	listed, _, listErr := plate.NewStore(filepath.Join(dir, "state", "plate")).List(plate.Query{From: plate.Day(now.AddDate(0, 0, -2)), To: plate.Day(now)})
	if listErr != nil || len(listed) != 1 {
		t.Fatalf("fixture records = %d, %v", len(listed), listErr)
	}
	loaded, loadErr := loadConfig(conf)
	_, payloadOK := payload(listed[0], false)
	if loadErr != nil || !loaded.Enabled || loaded.CutoverAt.IsZero() || !payloadOK || listed[0].FirstSeen.Before(loaded.CutoverAt) || now.Sub(listed[0].LastSeen) < defaultGrace {
		t.Fatalf("fixture rejected: config=%+v load=%v payload=%v first=%v last=%v", loaded, loadErr, payloadOK, listed[0].FirstSeen, listed[0].LastSeen)
	}

	s := New(conf, filepath.Join(dir, "state"))
	s.Client = client
	s.Clock = func() time.Time { return now }
	n, err := s.SyncOnce(context.Background())
	if err != nil || n != 1 {
		t.Fatalf("sync = %d, %v state=%+v", n, err, s.loadState())
	}
	if got["event_id"] == "" || got["observed_at"] == "" {
		t.Fatalf("missing fields: %#v", got)
	}
	if _, ok := got["location"]; ok {
		t.Fatal("location leaked")
	}
	if _, ok := got["image"]; ok {
		t.Fatal("image leaked")
	}
	result, ok := Results(filepath.Join(dir, "state"))[record.ID]
	if !ok || result.Status != "exact" || result.Category != "危険" || result.Comment != "管理者コメント" || !result.MetadataPresent || result.CheckedAt.IsZero() {
		t.Fatalf("user-visible result missing: %+v, %v", result, ok)
	}
	// The same record is not sent twice.
	if n, err = s.SyncOnce(context.Background()); err != nil || n != 0 {
		t.Fatalf("second sync = %d, %v", n, err)
	}
}

func TestPayloadSendsLocationOnlyWhenExplicitlyEnabled(t *testing.T) {
	accuracy := 8.5
	record := plate.Record{
		ID:        "plocation-0001",
		FirstSeen: time.Date(2026, 11, 16, 2, 0, 0, 0, time.UTC),
		Reading:   plate.Reading{Number: "1234"},
		Location:  &plate.Location{Lat: 35.0, Lon: 139.0, AccuracyM: &accuracy, Estimated: true},
	}
	without, ok := payload(record, false)
	if !ok || without.Location != nil {
		t.Fatalf("location sent without opt-in: %+v", without.Location)
	}
	with, ok := payload(record, true)
	if !ok || with.Location == nil || with.Location.Latitude != 35.0 || with.Location.Longitude != 139.0 || with.Location.AccuracyM == nil || *with.Location.AccuracyM != accuracy {
		t.Fatalf("opt-in location missing: %+v", with.Location)
	}
	b, err := json.Marshal(with)
	if err != nil {
		t.Fatal(err)
	}
	text := string(b)
	if !strings.Contains(text, `"location":{"latitude":35,"longitude":139,"accuracy_m":8.5}`) || strings.Contains(text, `"estimated"`) || strings.Contains(text, `"at"`) {
		t.Fatalf("unexpected location payload: %s", text)
	}
}

func writeJSONLine(t *testing.T, path string, value any) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(value)
	if err := os.WriteFile(path, append(b, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestSyncOnceDoesNotBackfillOrSendUnsettledRecord(t *testing.T) {
	now := time.Date(2026, 11, 16, 1, 0, 0, 0, time.UTC)
	requests := 0
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		requests++
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"saved":true,"match":{"status":"none","plate":null}}`)), Header: make(http.Header)}, nil
	})}
	dir := t.TempDir()
	cutover := now.Add(-time.Hour)
	conf := filepath.Join(dir, "numbershare.json")
	writeJSON(t, conf, Config{Enabled: true, BaseURL: "https://numbershare.test/api/", APIKey: "key", CutoverAt: cutover})
	records := []plate.Record{
		{ID: "pold-0001", FirstSeen: cutover.Add(-time.Minute), LastSeen: cutover.Add(-time.Minute), Reading: plate.Reading{Number: "1"}},
		{ID: "popen-0001", FirstSeen: now.Add(-time.Minute), LastSeen: now.Add(-10 * time.Second), Reading: plate.Reading{Number: "2"}},
	}
	for _, r := range records {
		writeJSONLine(t, filepath.Join(dir, "state", "plate", "records", plate.Day(r.FirstSeen)+".jsonl"), r)
	}
	s := New(conf, filepath.Join(dir, "state"))
	s.Client = client
	s.Clock = func() time.Time { return now }
	if n, err := s.SyncOnce(context.Background()); err != nil || n != 0 || requests != 0 {
		t.Fatalf("sync=%d requests=%d err=%v", n, requests, err)
	}
}

func TestEventIDIsStableUUIDv4(t *testing.T) {
	a, b := eventID("pstable-0001"), eventID("pstable-0001")
	if a != b || len(a) != 36 || a[14] != '4' || (a[19] != '8' && a[19] != '9' && a[19] != 'a' && a[19] != 'b') {
		t.Fatalf("bad UUID %q %q", a, b)
	}
}

func TestCheckAuthenticatesWithoutReturningPrivateBody(t *testing.T) {
	dir := t.TempDir()
	conf := filepath.Join(dir, "numbershare.json")
	writeJSON(t, conf, Config{Enabled: true, BaseURL: "https://numbershare.test/api/", APIKey: "device-secret", CutoverAt: time.Now().Add(-time.Minute)})
	s := New(conf, filepath.Join(dir, "state"))
	s.Client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/users/me" || r.Header.Get("X-API-Key") != "device-secret" {
			t.Fatalf("check request mismatch")
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"private":"must-not-be-returned"}`)), Header: make(http.Header)}, nil
	})}
	result, err := s.Check(context.Background())
	if err != nil || !result.Reachable || !result.Authenticated || result.Status != "ok" {
		t.Fatalf("check=%+v err=%v", result, err)
	}
	encoded, _ := json.Marshal(result)
	if strings.Contains(string(encoded), "private") || strings.Contains(string(encoded), "device-secret") {
		t.Fatal("private response or key escaped check result")
	}
}

func TestResponseResultSeparatesExactCandidateAndInvalidMetadata(t *testing.T) {
	at := time.Date(2026, 11, 17, 3, 0, 0, 0, time.UTC)
	exact, err := responseResult(serverResponse{Saved: true, Match: serverMatch{Status: "exact", Plate: &serverPlate{Category: " 警察 ", Comment: " 登録情報 ", MetadataPresent: true}}}, at)
	if err != nil || exact.Status != "exact" || exact.Category != "警察" || exact.Comment != "登録情報" || exact.CheckedAt != at {
		t.Fatalf("exact result = %+v, %v", exact, err)
	}
	candidate, err := responseResult(serverResponse{Saved: true, Match: serverMatch{Status: "candidate", Plate: &serverPlate{Category: "危険", Comment: "must not escape"}}}, at)
	if err != nil || candidate.Status != "candidate" || candidate.Category != "" || candidate.Comment != "" || candidate.MetadataPresent {
		t.Fatalf("candidate metadata escaped: %+v, %v", candidate, err)
	}
	for name, value := range map[string]serverResponse{
		"unsaved":        {Match: serverMatch{Status: "none"}},
		"missing-status": {Saved: true},
		"exact-no-plate": {Saved: true, Match: serverMatch{Status: "exact"}},
		"unknown-status": {Saved: true, Match: serverMatch{Status: "maybe"}},
	} {
		if result, err := responseResult(value, at); err == nil || result != nil {
			t.Fatalf("%s accepted: %+v, %v", name, result, err)
		}
	}
}
