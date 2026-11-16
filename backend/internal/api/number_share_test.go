package api

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestNumberShareConfigIsWriteOnlyAndRevisioned(t *testing.T) {
	srv, ts, client, csrf := plateFixture(t, &fakeSensor{instance: "share-test"})
	key := "fictional-secret-key"
	if _, err := srv.numberShare.Configure(1, true, "https://share.example/api/", &key); err != nil {
		t.Fatal(err)
	}
	res := request(t, client, ts, http.MethodGet, "/plate/share/status", csrf, nil)
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusOK || strings.Contains(string(body), key) || strings.Contains(string(body), "apiKey") {
		t.Fatalf("status %d leaked secret: %s", res.StatusCode, body)
	}
	var status struct {
		Config struct {
			Revision      int  `json:"revision"`
			KeyConfigured bool `json:"keyConfigured"`
		} `json:"config"`
	}
	if err := json.Unmarshal(body, &status); err != nil || status.Config.Revision != 2 || !status.Config.KeyConfigured {
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
