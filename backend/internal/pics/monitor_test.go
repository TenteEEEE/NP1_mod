package pics

import (
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
)

func frame(id byte, lamps ...byte) []byte {
	return append([]byte{0, 0, 2, 255, 0, 0, 0, 0, 0, id}, lamps...)
}

func TestCountdownUsesObservedCyclesAndBlueUsesAdvertisement(t *testing.T) {
	var m Monitor
	now := time.Now()
	red := []byte{0x71, 0x61, 0x51, 0x41, 0x31, 0x21, 0x11, 0x01}
	stepsAt := []int{0, 1, 6, 11, 16, 20, 24, 26}
	var heard []string
	for cycle := 0; cycle <= timingSamples; cycle++ {
		for sec := 0; sec <= 28; sec++ {
			var state byte = 0x83
			if sec < 28 {
				for i, change := range stepsAt {
					if sec >= change {
						state = red[i]
					}
				}
			}
			m.Receive("first", frame(1, state, 0x81, 0x84), now)
			if cycle == 0 && sec < 28 && m.Signals(now)[0].Seconds != nil {
				t.Fatal("first observation produced unverified seconds")
			}
			if cue, ok := m.NextCue(now); ok {
				if !m.Valid(cue, now) {
					t.Fatal("fresh cue rejected", cue)
				}
				if cycle == timingSamples {
					heard = append(heard, fmt.Sprintf("%s:%d", cue.Kind, cue.Seconds))
				}
				m.Claim(cue, now)
			} else if sec == 28 {
				t.Fatal("blue change was missed")
			}
			now = now.Add(time.Second)
		}
	}
	want := []string{"remaining:27", "remaining:10", "number:5", "number:4", "number:3", "number:2", "number:1", "green:0"}
	if !slices.Equal(heard, want) {
		t.Fatalf("heard %v, want %v", heard, want)
	}
}

func TestFirstVisitUsesEverySlotAndIgnoresStaleChanges(t *testing.T) {
	var m Monitor
	now := time.Now()
	m.Receive("west", frame(1, 0x84, 0x81, 0x81), now)
	m.Receive("east", frame(2, 0x81), now)
	m.Receive("east", frame(2, 0x83), now.Add(2*time.Second))
	if _, ok := m.NextCue(now.Add(2 * time.Second)); ok {
		t.Fatal("a transition inferred across a gap")
	}
	m.Receive("west", frame(1, 0x84, 0x81, 0x81), now.Add(2*time.Second))
	m.Receive("west", frame(1, 0x84, 0x81, 0x83), now.Add(2500*time.Millisecond))
	cue, ok := m.NextCue(now.Add(2500 * time.Millisecond))
	if !ok || cue.Kind != "green" || cue.slot != 2 {
		t.Fatalf("slot 3 green cue missing: %#v", cue)
	}
	m.Receive("second sender", frame(1, 0x84, 0x81, 0x81), now.Add(2600*time.Millisecond))
	if m.StillValid(cue, now.Add(2600*time.Millisecond)) {
		t.Fatal("conflicting packet did not invalidate cue")
	}
}

func TestMetadataAndRSSIHaveNoSignalFreshness(t *testing.T) {
	var m Monitor
	now := time.Now()
	m.Receive("device", frame(1, 0x83), now)
	name := make([]byte, 24)
	name[9] = 1
	copy(name[10:], "Test crossing")
	m.Receive("device", name, now.Add(time.Second))
	m.Receive("device", frame(1), now.Add(4*time.Second))
	if got := m.Signals(now.Add(6 * time.Second)); len(got) != 0 {
		t.Fatalf("metadata refreshed a stale signal: %#v", got)
	}
	adapter := dbus.ObjectPath("/org/bluez/hci0")
	event := &dbus.Signal{Path: adapter + "/dev_TEST", Name: propertiesInterface + ".PropertiesChanged", Body: []any{deviceInterface, map[string]dbus.Variant{"RSSI": dbus.MakeVariant(int16(-30))}}}
	if _, payload := advertisement(event, adapter); payload != nil {
		t.Fatal("RSSI notification was treated as advertising")
	}
	event.Body[1] = map[string]dbus.Variant{"ManufacturerData": dbus.MakeVariant(map[uint16]dbus.Variant{Manufacturer: dbus.MakeVariant(frame(1, 0x83))})}
	if _, payload := advertisement(event, adapter); len(payload) != 11 {
		t.Fatal("PICS advertising was not decoded")
	}
}
