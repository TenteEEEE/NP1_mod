package led

import (
	"context"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestTrafficColorsUseDistinctZonesAndExpire(t *testing.T) {
	path := filepath.Join(t.TempDir(), "traffic.json")
	now := time.Unix(100, 0)
	if err := writeTrafficColors(path, TrafficRed|TrafficBlinkGreen|TrafficGreen, now.Add(2*time.Second), now.Add(10*time.Second), now); err != nil {
		t.Fatal(err)
	}
	frame, active, err := readTrafficFrame(path, now)
	want := [5][3]int{{112, 0, 0}, {112, 0, 0}, {0, 112, 84}, {0, 112, 84}, {0, 112, 84}}
	if err != nil || !active || frame.Zones != want {
		t.Fatalf("unexpected traffic frame: %v active=%v err=%v", frame.Zones, active, err)
	}
	frame, active, err = readTrafficFrame(path, now.Add(500*time.Millisecond))
	want[2] = [3]int{}
	if err != nil || !active || frame.Zones != want {
		t.Fatalf("blink affected another zone: %v active=%v err=%v", frame.Zones, active, err)
	}
	if _, active, err := readTrafficFrame(path, now.Add(trafficLease)); err != nil || active {
		t.Fatalf("traffic frame did not expire: active=%v err=%v", active, err)
	}
}

func TestTrafficYieldsToScreenOverlayAndResumes(t *testing.T) {
	f := newManagedFixture()
	d := f.dependencies()
	var screenOn, trafficOn atomic.Bool
	trafficOn.Store(true)
	d.screen = func(time.Time) (ScreenFrame, bool, error) {
		return ScreenFrame{ScreenUpdate: ScreenUpdate{Sequence: 1, Zones: [5][3]int{{255, 255, 255}}}}, screenOn.Load(), nil
	}
	d.traffic = func(time.Time) (ScreenFrame, bool, error) {
		return ScreenFrame{ScreenUpdate: ScreenUpdate{Sequence: 2, Zones: [5][3]int{{112, 0, 0}}}}, trafficOn.Load(), nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runManaged(ctx, d) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
	})
	f.wait(t, func(s State) bool { return s.Mode == "traffic" && s.ScreenSequence == 2 })
	screenOn.Store(true)
	f.wait(t, func(s State) bool { return s.Mode == "screen" && s.ScreenSequence == 1 })
	screenOn.Store(false)
	f.wait(t, func(s State) bool { return s.Mode == "traffic" && s.ScreenSequence == 2 })
	trafficOn.Store(false)
	f.wait(t, func(s State) bool { return s.Mode == "solid" && s.ScreenSequence == 0 })
}

func TestDelayedTrafficWriteCannotExtendSourceDeadline(t *testing.T) {
	path := filepath.Join(t.TempDir(), "traffic.json")
	deadline := time.Unix(100, 0).Add(1250 * time.Millisecond)
	if err := writeTrafficColors(path, TrafficRed, deadline, deadline, deadline.Add(-50*time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	if _, active, err := readTrafficFrame(path, deadline); err != nil || active {
		t.Fatalf("source expired: active=%v err=%v", active, err)
	}
	if err := writeTrafficColors(path, TrafficGreen, deadline, deadline, deadline.Add(300*time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	if _, active, err := readTrafficFrame(path, deadline.Add(310*time.Millisecond)); err != nil || active {
		t.Fatalf("delayed request relit the LED: active=%v err=%v", active, err)
	}
}

func TestTrafficWaitsInTheDarkUntilVisitEnds(t *testing.T) {
	path := filepath.Join(t.TempDir(), "traffic.json")
	now := time.Unix(100, 0)
	colorUntil, visitUntil := now.Add(300*time.Millisecond), now.Add(2*time.Second)
	if err := writeTrafficColors(path, TrafficGreen, colorUntil, visitUntil, now); err != nil {
		t.Fatal(err)
	}
	if frame, active, err := readTrafficFrame(path, colorUntil); err != nil || !active || frame.Zones != [5][3]int{} {
		t.Fatalf("expired green did not leave the LED dark: %v %v %v", frame.Zones, active, err)
	}
	if err := writeTrafficColors(path, 0, time.Time{}, visitUntil, visitUntil.Add(-250*time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	if _, active, err := readTrafficFrame(path, visitUntil); err != nil || active {
		t.Fatalf("base LED did not resume after the visit: %v %v", active, err)
	}
}
