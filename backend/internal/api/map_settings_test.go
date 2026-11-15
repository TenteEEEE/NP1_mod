package api

import (
	"encoding/json"
	"io"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/np1-control-center/ncc/internal/config"
)

func TestMapThemesPersistIndependentlyAndRejectInvalidValues(t *testing.T) {
	cfg := config.Config{StateDir: t.TempDir(), NetworkTrust: true}
	server := New(cfg)
	httpServer := httptest.NewTLSServer(server.Handler())
	defer httpServer.Close()
	client := httpServer.Client()
	client.Jar, _ = cookiejar.New(nil)
	res := request(t, client, httpServer, "GET", "/auth/session", "", nil)
	var session struct {
		CSRF string `json:"csrf"`
	}
	if err := json.NewDecoder(res.Body).Decode(&session); err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	preferences := MapThemes{Dashboard: "colorful", Journey: "night", Playback: "mist", Plates: "sage"}
	settings := server.settings
	settings.MapThemes = preferences
	res = request(t, client, httpServer, "PUT", "/settings", session.CSRF, map[string]any{"expectedRevision": 1, "settings": settings})
	raw, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("save failed: %d %s", res.StatusCode, raw)
	}
	if got := New(cfg).settings.MapThemes; got != preferences {
		t.Fatalf("map choices lost after restart: %+v", got)
	}
	for _, field := range []string{"dashboard", "journey", "playback", "plates"} {
		var value map[string]any
		encoded, _ := json.Marshal(settings)
		json.Unmarshal(encoded, &value)
		value["mapThemes"].(map[string]any)[field] = "unrecognized"
		res = request(t, client, httpServer, "PUT", "/settings", session.CSRF, map[string]any{"expectedRevision": 2, "settings": value})
		res.Body.Close()
		if res.StatusCode != 400 {
			t.Errorf("invalid %s map theme accepted", field)
		}
	}
	if New(cfg).settings.MapThemes != preferences {
		t.Fatal("invalid request changed saved choices")
	}
}

func TestVectorMapCSPAllowsSameOriginWorkerAndOnlyTileHost(t *testing.T) {
	server, client, _ := fixture(t)
	response := request(t, client, server, "GET", "/auth/session", "", nil)
	defer response.Body.Close()
	policy := response.Header.Get("Content-Security-Policy")
	for _, rule := range []string{"worker-src 'self'", "connect-src 'self' https://tiles.openfreemap.org", "script-src 'self'"} {
		if !strings.Contains(policy, rule) {
			t.Errorf("missing vector map CSP rule %s", rule)
		}
	}
	if strings.Contains(policy, "unsafe-eval") || strings.Contains(policy, "tile.openstreetmap.org") {
		t.Fatal("obsolete or unsafe map policy", policy)
	}
}
