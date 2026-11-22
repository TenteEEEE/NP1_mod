package adapters

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"
)

func TestUplinkSwitchesToCellularOnlyAfterAPersistentOutage(t *testing.T) {
	t0 := time.Date(2026, 11, 14, 1, 0, 0, 0, time.UTC)
	home := &UplinkPosition{At: t0, Lat: 35.48, Lon: 139.41}
	var s UplinkState
	a, _, s := DecideUplink(UplinkInputs{Now: t0, STAOn: true, Working: true, Enabled: true, Position: home}, s)
	if a != UplinkNone || s.LastConnectedPos == nil {
		t.Fatalf("connected: %v %+v", a, s)
	}
	for _, delay := range []time.Duration{0, 10 * time.Second, uplinkOffAfter - time.Second} {
		if a, _, s = DecideUplink(UplinkInputs{Now: t0.Add(delay), STAOn: true, Enabled: true}, s); a != UplinkNone {
			t.Fatalf("switched too early at %v", delay)
		}
	}
	a, reason, s := DecideUplink(UplinkInputs{Now: t0.Add(uplinkOffAfter), STAOn: true, Enabled: true}, s)
	if a != UplinkOff || reason != "wifi-unavailable" || s.AutoOffAt == nil {
		t.Fatalf("expected OFF: %v %s %+v", a, reason, s)
	}
	far := &UplinkPosition{At: t0.Add(time.Minute), Lat: 35.60, Lon: 139.70}
	if a, _, s = DecideUplink(UplinkInputs{Now: far.At, STAOn: false, Enabled: true, Position: far}, s); a != UplinkNone {
		t.Fatal("must stay on 4G far from home")
	}
	// Back home: switch Wi-Fi on.
	back := &UplinkPosition{At: t0.Add(30 * time.Minute), Lat: 35.4801, Lon: 139.4101}
	a, reason, s = DecideUplink(UplinkInputs{Now: t0.Add(30 * time.Minute), STAOn: false, Enabled: true, Position: back}, s)
	if a != UplinkOn || reason != "near-last-connected-place" {
		t.Fatalf("expected ON near home: %v %s", a, reason)
	}
}
func TestUplinkPrefersAScanAndBacksOffAfterAFailedReturn(t *testing.T) {
	t0 := time.Date(2026, 11, 14, 1, 0, 0, 0, time.UTC)
	off := t0
	s := UplinkState{AutoOffAt: &off}
	if a, _, _ := DecideUplink(UplinkInputs{Now: t0, STAOn: false, Enabled: true, ScanKnown: true}, s); a != UplinkNone {
		t.Fatal("no saved network seen: stay on 4G")
	}
	a, reason, s := DecideUplink(UplinkInputs{Now: t0.Add(time.Minute), STAOn: false, Enabled: true, ScanKnown: true, SavedSeen: true}, s)
	if a != UplinkOn || reason != "saved-network-seen" {
		t.Fatalf("expected ON: %v %s", a, reason)
	}
	a = UplinkNone
	// The ON found nothing: after the outage delay it goes OFF again, then waits out the backoff.
	now := t0.Add(time.Minute)
	for i := 0; i < 30 && a != UplinkOff; i++ {
		now = now.Add(10 * time.Second)
		a, _, s = DecideUplink(UplinkInputs{Now: now, STAOn: true, Enabled: true}, s)
	}
	if a != UplinkOff {
		t.Fatalf("expected OFF after a failed return, got %v", a)
	}
	if a, _, _ = DecideUplink(UplinkInputs{Now: now.Add(time.Minute), STAOn: false, Enabled: true, ScanKnown: true, SavedSeen: true}, s); a != UplinkNone {
		t.Fatal("must not retry within the backoff")
	}
}
func TestUplinkKeepsWorkingWiFiWhenTheSupplicantReadsStale(t *testing.T) {
	t0 := time.Date(2026, 11, 14, 1, 0, 0, 0, time.UTC)
	// A service restart leaves the supplicant state unreadable for a while. The default
	// route still goes through Wi-Fi, so the supervisor must not switch to 4G.
	var s UplinkState
	var a UplinkAction
	for i := 0; i < 40; i++ {
		a, _, s = DecideUplink(UplinkInputs{Now: t0.Add(time.Duration(i) * 10 * time.Second), STAOn: true, Working: true, Enabled: true}, s)
		if a != UplinkNone {
			t.Fatalf("switched off while Wi-Fi carried the default route at %ds", i*10)
		}
	}
	// The same readings without a working uplink do switch off after the outage delay.
	s = UplinkState{}
	a = UplinkNone
	for i := 0; i <= 20 && a != UplinkOff; i++ {
		a, _, s = DecideUplink(UplinkInputs{Now: t0.Add(time.Duration(i) * 10 * time.Second), STAOn: true, Enabled: true}, s)
	}
	if a != UplinkOff {
		t.Fatalf("expected OFF without a working uplink, got %v", a)
	}
}
func TestUplinkProbesAgainWithoutScanOrFix(t *testing.T) {
	t0 := time.Date(2026, 11, 14, 1, 0, 0, 0, time.UTC)
	// Parked in a garage: no scan result (the station is off) and no GPS fix, so neither
	// rule can bring Wi-Fi back. A blind retry must happen eventually.
	off := t0
	s := UplinkState{AutoOffAt: &off}
	next := off.Add(uplinkProbeEvery)
	s.NextProbeAt = &next
	if a, _, _ := DecideUplink(UplinkInputs{Now: next.Add(-10 * time.Second), STAOn: false, Enabled: true}, s); a != UplinkNone {
		t.Fatal("probed before the interval")
	}
	a, reason, _ := DecideUplink(UplinkInputs{Now: next, STAOn: false, Enabled: true}, s)
	if a != UplinkOn || reason != "periodic-recheck" {
		t.Fatalf("expected a blind retry: %v %s", a, reason)
	}
	// Switching the owner's choice back to automatic schedules that retry for now,
	// so the station returns without waiting for the interval.
	now := t0
	if a, _, _ := DecideUplink(UplinkInputs{Now: now, STAOn: false, Enabled: true}, UplinkState{AutoOffAt: &off, NextProbeAt: &now}); a != UplinkOn {
		t.Fatalf("expected an immediate retry after choosing automatic, got %v", a)
	}
}
func TestUplinkNeverOverridesTheOwner(t *testing.T) {
	t0 := time.Date(2026, 11, 14, 1, 0, 0, 0, time.UTC)
	// The owner turned STA off in the stock settings: no marker, so never turned on.
	if a, _, _ := DecideUplink(UplinkInputs{Now: t0, STAOn: false, Enabled: true, ScanKnown: true, SavedSeen: true}, UplinkState{}); a != UplinkNone {
		t.Fatal("owner's OFF overridden")
	}
	// Automation disabled: never switches off, and restores a mode it had switched off.
	var s UplinkState
	for i := 0; i < 10; i++ {
		var a UplinkAction
		a, _, s = DecideUplink(UplinkInputs{Now: t0.Add(time.Duration(i) * 30 * time.Second), STAOn: true, Enabled: false}, s)
		if a != UplinkNone {
			t.Fatal("disabled automation switched")
		}
	}
	off := t0
	if a, reason, s := DecideUplink(UplinkInputs{Now: t0, STAOn: false, Enabled: false}, UplinkState{AutoOffAt: &off}); a != UplinkOn || reason != "auto-disabled" || s.AutoOffAt != nil {
		t.Fatalf("disabling must restore Wi-Fi: %v %s", a, reason)
	}
	// The owner switched it back on: the marker is dropped.
	if _, _, s := DecideUplink(UplinkInputs{Now: t0, STAOn: true, Working: true, Enabled: true}, UplinkState{AutoOffAt: &off}); s.AutoOffAt != nil {
		t.Fatal("marker kept after ON")
	}
}

func TestStationLinkUpIgnoresAStaleDefaultRoute(t *testing.T) {
	if stationLinkUp("down", "0", true) {
		t.Fatal("a disconnected station must not count as working")
	}
	if stationLinkUp("up", "0", true) {
		t.Fatal("carrier 0 must not count as working")
	}
	if !stationLinkUp("up", "1", true) || !stationLinkUp("up", "", false) {
		t.Fatal("a connected station must count as working")
	}
}

func TestUplinkBlindProbeGivesTheRadioBackWhenNothingConnects(t *testing.T) {
	t0 := time.Date(2026, 11, 14, 1, 0, 0, 0, time.UTC)
	off := t0
	s := UplinkState{AutoOffAt: &off}
	probe := t0
	s.NextProbeAt = &probe
	a, reason, s := DecideUplink(UplinkInputs{Now: t0, STAOn: false, Enabled: true}, s)
	if a != UplinkOn || reason != "periodic-recheck" || s.ProbeUntil == nil {
		t.Fatalf("expected a bounded probe: %v %s %+v", a, reason, s)
	}
	// Inside the window nothing is decided, even though the station looks unconnected.
	for _, delay := range []time.Duration{5 * time.Second, 20 * time.Second, uplinkProbeWindow - time.Second} {
		if a, _, s = DecideUplink(UplinkInputs{Now: t0.Add(delay), STAOn: true, Enabled: true}, s); a != UplinkNone {
			t.Fatalf("gave up inside the probe window at %v: %v", delay, a)
		}
	}
	// After the window the station goes back off at once, not after the outage delay.
	a, reason, s = DecideUplink(UplinkInputs{Now: t0.Add(uplinkProbeWindow), STAOn: true, Enabled: true}, s)
	if a != UplinkOff || reason != "probe-timeout" || s.AutoOffAt == nil || s.ProbeUntil != nil {
		t.Fatalf("expected probe-timeout OFF: %v %s %+v", a, reason, s)
	}
	if s.NextProbeAt == nil || !s.NextProbeAt.Equal(t0.Add(uplinkProbeWindow+uplinkProbeEvery)) {
		t.Fatalf("next probe not scheduled: %+v", s.NextProbeAt)
	}
	// A probe that does associate keeps the station on.
	probe2 := t0.Add(4 * time.Minute)
	keep := UplinkState{AutoOffAt: &off, NextProbeAt: &probe2, ProbeUntil: &probe2}
	if a, _, keep = DecideUplink(UplinkInputs{Now: probe2, STAOn: false, Enabled: true}, keep); a != UplinkOn {
		t.Fatalf("expected probe: %v", a)
	}
	if a, _, keep = DecideUplink(UplinkInputs{Now: probe2.Add(10 * time.Second), STAOn: true, Working: true, Enabled: true}, keep); a != UplinkNone || keep.ProbeUntil != nil || keep.AutoOffAt != nil {
		t.Fatalf("connected probe kept the probe markers: %v %+v", a, keep)
	}
}

func TestUplinkInterruptedProbeSurvivesRebootClockRollback(t *testing.T) {
	now := time.Date(2026, 11, 22, 0, 0, 0, 0, time.UTC)
	future := now.Add(24 * time.Hour)
	action, reason, _ := DecideUplink(UplinkInputs{Now: now, STAOn: true, Enabled: true}, UplinkState{ProbeUntil: &future})
	if action != UplinkOff || reason != "probe-timeout" {
		t.Fatalf("future deadline left STA stranded: %v %s", action, reason)
	}
	_, _, state := DecideUplink(UplinkInputs{Now: now, Enabled: true}, UplinkState{ProbeUntil: &future, RetryAfter: &future})
	if state.AutoOffAt == nil || state.NextProbeAt == nil || state.NextProbeAt.Sub(now) > uplinkProbeEvery {
		t.Fatalf("interrupted return lost automation: %+v", state)
	}
	if action, _, _ := DecideUplink(UplinkInputs{Now: now.Add(uplinkProbeEvery), Enabled: true, NoTargets: true}, state); action != UplinkNone {
		t.Fatal("empty inventory should not interrupt LTE")
	}
}

func TestUplinkOnlyReconcilesVerifiedOffMismatchWithoutLiveWiFi(t *testing.T) {
	tests := []struct {
		name, initial, next string
		link, repair        bool
	}{
		{"known mismatch", "STA read-back stage=STA-get owner=2 native40=0\n", "2", false, true},
		{"live link", "STA read-back stage=STA-get owner=2 native40=0\n", "2", true, false},
		{"opposite mismatch", "STA read-back stage=STA-get owner=1 native40=1\n", "2", false, false},
		{"apply already sent", "STA read-back stage=STA-readback owner=2 native40=0\n", "2", false, false},
		{"request to turn on", "STA read-back stage=STA-get owner=2 native40=0\n", "1", false, false},
		{"other error", "correlated Communication STA response timed out\n", "2", false, false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var calls []string
			_, err := applySTA(context.Background(), "42", "1", test.next, func() bool { return test.link }, func(_ context.Context, args ...string) ([]byte, error) {
				calls = append(calls, args[0])
				if args[0] == "--reconcile" {
					return []byte("wifi-sta-wire=2\n"), nil
				}
				return []byte(test.initial), errors.New("unconfirmed")
			})
			want := []string{"--apply"}
			if test.repair {
				want = append(want, "--reconcile")
			}
			if !slices.Equal(calls, want) || (err == nil) != test.repair {
				t.Fatalf("calls %v, err %v", calls, err)
			}
		})
	}
}

func TestUplinkRequiresInternetAndSurvivesOneProbeMiss(t *testing.T) {
	now := time.Date(2026, 11, 22, 0, 0, 0, 0, time.UTC)
	var health uplinkWiFiHealth
	ok, fail := func() bool { return true }, func() bool { return false }
	if health.working(now, true, fail) || !health.working(now.Add(uplinkWiFiCheckEvery), true, ok) {
		t.Fatal("link was accepted without Internet or rejected with proof")
	}
	if !health.working(now.Add(2*uplinkWiFiCheckEvery), true, fail) || health.working(now.Add(3*uplinkWiFiCheckEvery), true, fail) {
		t.Fatal("probe retries did not distinguish a transient from an outage")
	}
	if health.working(now.Add(3*uplinkWiFiCheckEvery), false, ok) || !health.working(now.Add(3*uplinkWiFiCheckEvery+time.Second), true, ok) {
		t.Fatal("link change must invalidate cached proof immediately")
	}
}
