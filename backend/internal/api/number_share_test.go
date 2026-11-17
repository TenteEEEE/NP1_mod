package api

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/np1-control-center/ncc/internal/plate"
)

func TestNumberShareConfigReturnsSavedKeyAndIsRevisioned(t *testing.T) {
	srv, ts, client, csrf := plateFixture(t, &fakeSensor{instance: "share-test"})
	key := "fictional-secret-key"
	if _, err := srv.numberShare.Configure(1, true, "https://share.example/api/", &key); err != nil {
		t.Fatal(err)
	}
	res := request(t, client, ts, http.MethodGet, "/plate/share/status", csrf, nil)
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusOK || !strings.Contains(string(body), key) || !strings.Contains(string(body), "apiKey") {
		t.Fatalf("status %d did not return saved key", res.StatusCode)
	}
	var status struct {
		Config struct {
			Revision      int    `json:"revision"`
			KeyConfigured bool   `json:"keyConfigured"`
			APIKey        string `json:"apiKey"`
		} `json:"config"`
	}
	if err := json.Unmarshal(body, &status); err != nil || status.Config.Revision != 2 || !status.Config.KeyConfigured || status.Config.APIKey != key {
		t.Fatalf("status = %+v, %v", status, err)
	}
	// Omitting key preserves it, and stale updates are rejected.
	res = request(t, client, ts, http.MethodPut, "/plate/share/config", csrf, map[string]any{"expectedRevision": 2, "enabled": true, "endpoint": "https://share.example/v2/"})
	res.Body.Close()
	if res.StatusCode != http.StatusOK || !srv.numberShare.Config().KeyConfigured {
		t.Fatalf("update status=%d config=%+v", res.StatusCode, srv.numberShare.Config())
	}
	res = request(t, client, ts, http.MethodPut, "/plate/share/config", csrf, map[string]any{"expectedRevision": 2, "enabled": false, "endpoint": ""})
	res.Body.Close()
	if res.StatusCode != http.StatusConflict {
		t.Fatalf("stale status=%d", res.StatusCode)
	}
}

func TestNumberShareRejectsInvalidQueriesBeforeExternalRequest(t *testing.T) {
	_, ts, client, csrf := plateFixture(t, &fakeSensor{instance: "share-query"})
	for _, path := range []string{
		"/plate/share/sightings?scope=somebody",
		"/plate/share/sightings?matchStatus=maybe",
		"/plate/share/sightings?unknown=x",
		"/plate/share/vehicles?q=a&q=b",
	} {
		res := request(t, client, ts, http.MethodGet, path, csrf, nil)
		res.Body.Close()
		if res.StatusCode != http.StatusBadRequest {
			t.Errorf("%s status=%d", path, res.StatusCode)
		}
	}
}

func TestNumberShareUploadRequiresExplicitValidPeriod(t *testing.T) {
	_, ts, client, csrf := plateFixture(t, &fakeSensor{instance: "share-upload"})
	for _, body := range []map[string]any{
		{"from": "", "to": "2026-11-16"},
		{"from": "2026-11-17", "to": "2026-11-16"},
		{"from": "2026/11/16", "to": "2026-11-16"},
	} {
		res := request(t, client, ts, http.MethodPost, "/plate/share/upload", csrf, body)
		res.Body.Close()
		if res.StatusCode != http.StatusBadRequest {
			t.Fatalf("body=%v status=%d", body, res.StatusCode)
		}
	}
}

func TestNumberShareUploadQueuesEveryRecordInPeriodOnce(t *testing.T) {
	srv, ts, client, csrf := plateFixture(t, &fakeSensor{instance: "share-upload-success"})
	key := "fictional-upload-key"
	if _, err := srv.numberShare.Configure(1, false, "https://share.example/api/", &key); err != nil {
		t.Fatal(err)
	}
	settings := plate.DefaultSettings()
	settings.MinSightings = 1
	at := time.Date(2026, 11, 16, 12, 0, 0, 0, time.FixedZone("JST", 9*3600))
	if outcome, err := srv.plates.store.Ingest(plate.Detection{At: at, Reading: plate.Reading{Number: "1234", Confidence: 0.99}}, settings, nil); err != nil || !outcome.New {
		t.Fatalf("ingest = %+v, %v", outcome, err)
	}
	for attempt, expected := range []struct{ queued, existing int }{{1, 0}, {0, 1}} {
		res := request(t, client, ts, http.MethodPost, "/plate/share/upload", csrf, map[string]any{"from": "2026-11-16", "to": "2026-11-16"})
		var result struct {
			Total, Queued, Existing int
		}
		_ = json.NewDecoder(res.Body).Decode(&result)
		res.Body.Close()
		if res.StatusCode != http.StatusAccepted || result.Total != 1 || result.Queued != expected.queued || result.Existing != expected.existing {
			t.Fatalf("attempt %d status=%d result=%+v", attempt, res.StatusCode, result)
		}
	}
	status := srv.numberShare.Status()
	if status.Pending != 1 || len(status.Items) != 1 || !status.Items[0].Manual {
		t.Fatalf("queue = %+v", status)
	}
}
