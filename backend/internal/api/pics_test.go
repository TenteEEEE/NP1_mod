package api

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"
)

func TestPICSDefaultsAndSave(t *testing.T) {
	s, client, _ := fixture(t)
	csrf := login(t, client, s)
	load := func() picsSettings {
		t.Helper()
		r := request(t, client, s, "GET", "/pics", csrf, nil)
		defer r.Body.Close()
		var data struct{ Settings picsSettings }
		if r.StatusCode != http.StatusOK || json.NewDecoder(r.Body).Decode(&data) != nil {
			t.Fatalf("GET /pics: %d", r.StatusCode)
		}
		return data.Settings
	}
	if cfg := load(); !cfg.Enabled || cfg.Speech {
		t.Fatalf("defaults: %#v", cfg)
	}
	for _, attempt := range []struct {
		cfg  picsSettings
		want int
	}{{picsSettings{Enabled: true, Volume: 101}, http.StatusBadRequest}, {picsSettings{Speech: true, Style: 3, Volume: 40}, http.StatusOK}} {
		r := request(t, client, s, "PUT", "/pics", csrf, attempt.cfg)
		r.Body.Close()
		if r.StatusCode != attempt.want {
			t.Fatalf("save: got %d, want %d", r.StatusCode, attempt.want)
		}
	}
	if cfg := load(); cfg.Enabled || !cfg.Speech || cfg.Style != 3 || cfg.Volume != 40 {
		t.Fatalf("saved config: %#v", cfg)
	}
}

func TestPICSDisableReleasesScannerAndPersists(t *testing.T) {
	dir := t.TempDir()
	p := newPICSService(dir, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started, stopped := make(chan struct{}), make(chan struct{})
	p.scan = func(ctx context.Context, ready func(), _ func(string, []byte)) error {
		ready()
		close(started)
		<-ctx.Done()
		close(stopped)
		return ctx.Err()
	}
	go p.runScan(ctx)
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("default-enabled scanner did not start")
	}
	settings := defaultPICSSettings()
	settings.Enabled = false
	if err := p.save(settings); err != nil {
		t.Fatal(err)
	}
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("disabled scanner did not stop")
	}
	if reloaded := newPICSService(dir, nil); reloaded.settings != settings {
		t.Fatalf("disabled state not persisted: %#v", reloaded.settings)
	}
}
