package api

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/np1-control-center/ncc/internal/pics"
	"github.com/np1-control-center/ncc/internal/trips"
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
	if cfg := load(); !cfg.Enabled || cfg.Speech || cfg.Directional {
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

func TestPICSDirectionRulesAreOptionalAndConfirmedByCourse(t *testing.T) {
	now := time.Now()
	dir := t.TempDir()
	p := newPICSService(dir, nil)
	if !p.directionAllows("00000001", 1, now) {
		t.Fatal("default configuration misclassified an unmapped direction")
	}
	p.settings.Directional = true
	if p.directionAllows("00000001", 1, now) {
		t.Fatal("enabled direction filter accepted an unknown crossing")
	}
	axis, course := 90.0, 270.0
	p.directions["00000001"] = picsAxes{&axis}
	p.positions = []trips.TrackPoint{{At: now}}
	p.course = &course
	if !p.directionAllows("00000001", 1, now) || p.directionAllows("00000001", 2, now) || p.directionAllows("00000001", 1, now.Add(6*time.Second)) {
		t.Fatal("axis, unknown slot or freshness matching is incorrect")
	}
	s, client, _ := fixture(t)
	csrf := login(t, client, s)
	res := request(t, client, s, "PUT", "/pics/directions/00000001", csrf, map[string]any{"axes": picsAxes{&axis}})
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("could not save the confirmed direction: %d", res.StatusCode)
	}
	res = request(t, client, s, "GET", "/pics/directions", csrf, nil)
	defer res.Body.Close()
	var output map[string]picsAxes
	if json.NewDecoder(res.Body).Decode(&output) != nil || output["00000001"][0] == nil || *output["00000001"][0] != axis {
		t.Fatal("stored direction not returned")
	}
}

func TestPICSHistoryRetainsUnplacedRecordsAndJoinsTheRightFix(t *testing.T) {
	dir := t.TempDir()
	now, _ := time.Parse(time.RFC3339, "2026-11-21T03:00:00Z")
	date := now.In(trips.Japan).Format("2006-01-02")
	entries := []picsReception{{Reception: pics.Reception{ID: "00000001", Slot: 1, Color: pics.Red, At: now}}, {Reception: pics.Reception{ID: "00000001", Slot: 2, Color: pics.Green, At: now.Add(20 * time.Second)}}}
	if err := appendPICSHistory(dir, date, entries); err != nil {
		t.Fatal(err)
	}
	entries, partial, err := readPICSHistory(filepath.Join(dir, date+".jsonl"))
	if err != nil || partial || len(entries) != 2 {
		t.Fatalf("history did not round trip: %v, %v, %d", err, partial, len(entries))
	}
	fillPICSPositions(entries, trips.TrackReport{Points: []trips.TrackPoint{{At: now, Lat: 10, Lon: 20}}})
	if entries[0].Position == nil || entries[0].Position.Lat != 10 || entries[1].Position != nil {
		t.Fatal("a matching position was lost or was assigned to the wrong record")
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
