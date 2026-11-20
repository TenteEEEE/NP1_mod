package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/np1-control-center/ncc/internal/led"
	"github.com/np1-control-center/ncc/internal/pics"
	"github.com/np1-control-center/ncc/internal/trips"
)

func TestPICSResumesScanningAfterAnInterruptedSession(t *testing.T) {
	p := newPICSService(t.TempDir(), nil)
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	resumed := make(chan struct{})
	calls := 0
	p.scan = func(ctx context.Context, ready func(), _ func(string, []byte, time.Time)) error {
		calls++
		if calls == 1 {
			return errors.New("stock BLE temporarily refused the scan")
		}
		ready()
		close(resumed)
		<-ctx.Done()
		return ctx.Err()
	}
	go p.runScan(ctx)
	select {
	case <-resumed:
		p.mu.Lock()
		defer p.mu.Unlock()
		if p.scanState != "scanning" || p.scanReason != "" {
			t.Fatalf("unexpected recovery state %q: %q", p.scanState, p.scanReason)
		}
	case <-ctx.Done():
		t.Fatal("Bluetooth recovery did not restart discovery")
	}
}

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
	if cfg := load(); !cfg.Enabled || cfg.Speech || cfg.Directional || cfg.LED {
		t.Fatalf("defaults: %#v", cfg)
	}
	for _, attempt := range []struct {
		cfg  picsSettings
		want int
	}{{picsSettings{Enabled: true, Volume: 101}, http.StatusBadRequest}, {picsSettings{Speech: true, LED: true, Style: 3, Volume: 40}, http.StatusOK}} {
		r := request(t, client, s, "PUT", "/pics", csrf, attempt.cfg)
		r.Body.Close()
		if r.StatusCode != attempt.want {
			t.Fatalf("save: got %d, want %d", r.StatusCode, attempt.want)
		}
	}
	if cfg := load(); cfg.Enabled || !cfg.Speech || !cfg.LED || cfg.Style != 3 || cfg.Volume != 40 {
		t.Fatalf("saved config: %#v", cfg)
	}
}

func TestPICSLightsFreshDirectionsOnly(t *testing.T) {
	p := newPICSService(t.TempDir(), nil)
	now := time.Now()
	p.scanState = "scanning"
	p.monitor.Receive("beacon", []byte{0, 0, 2, 0, 0, 0, 0, 0, 0, 1, 0x81, 0x82, 0x83}, now)
	if got, _ := p.trafficColors(now); got != 0 {
		t.Fatalf("disabled LED showed %v", got)
	}
	p.settings.LED = true
	if got, _ := p.trafficColors(now); got != led.TrafficRed|led.TrafficBlinkGreen|led.TrafficGreen {
		t.Fatalf("LED did not show all visible colors: %v", got)
	}
	axis, course := 90.0, 270.0
	p.settings.Directional = true
	p.directions["00000001"] = picsAxes{nil, nil, &axis}
	p.positions = []trips.TrackPoint{{At: now}}
	p.course = &course
	if got, _ := p.trafficColors(now); got != led.TrafficGreen {
		t.Fatalf("LED ignored the selected directions: %v", got)
	}
	if got, _ := p.trafficColors(now.Add(2 * time.Second)); got != 0 {
		t.Fatalf("expired signal still shown: %v", got)
	}
	p.scanState = "unavailable"
	if got, _ := p.trafficColors(now); got != 0 {
		t.Fatalf("interrupted scanner still shown: %v", got)
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
	p.scan = func(ctx context.Context, ready func(), _ func(string, []byte, time.Time)) error {
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
