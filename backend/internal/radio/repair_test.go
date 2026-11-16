package radio

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDelayedPositionsRepairBothRotationsWithoutChangingRadioOrExistingFix(t *testing.T) {
	dir := t.TempDir()
	h := NewHistory(dir)
	at := time.Date(2026, 11, 14, 3, 0, 0, 0, time.UTC)
	accuracy := 4.0
	positions := []Position{{At: at, Lat: 35, Lon: 139, AccuracyM: &accuracy, Source: "stock-recording-nmea"}}
	raw := []byte(`{"at":"2026-11-14T03:00:00Z","rsrp":-86,"source":"synthetic","futureField":{"keep":true}}` + "\n")
	for _, suffix := range []string{".1", ""} {
		if err := os.WriteFile(filepath.Join(dir, "radio-observations.jsonl"+suffix), raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	preview, err := h.RepairPositions(positions, false)
	if err != nil || preview.Matched != 2 || preview.Updated != 0 {
		t.Fatal(preview, err)
	}
	unchanged, _ := os.ReadFile(filepath.Join(dir, "radio-observations.jsonl"))
	if !bytes.Equal(unchanged, raw) {
		t.Fatal("preview mutated history")
	}
	report, err := h.RepairPositions(positions, true)
	if err != nil || report.Updated != 2 || report.FilesChanged != 2 {
		t.Fatal(report, err)
	}
	for _, suffix := range []string{".1", ""} {
		readback, _ := os.ReadFile(filepath.Join(dir, "radio-observations.jsonl"+suffix))
		var v map[string]json.RawMessage
		if json.Unmarshal(readback, &v) != nil || string(v["rsrp"]) != "-86" || string(v["futureField"]) != "{\"keep\":true}" || v["gps"] == nil {
			t.Fatal(string(readback))
		}
	}
	positions[0].Lon = 140
	again, err := h.RepairPositions(positions, true)
	if err != nil || again.Updated != 0 || again.AlreadyLocated != 2 {
		t.Fatal("not idempotent", again, err)
	}
	r, err := h.Query(Query{})
	if err != nil || len(r.Points) != 2 || r.Points[0].GPS.Lon != 139 {
		t.Fatal(r, err)
	}
}

func TestRepairMatchesNearestWithinFiveSecondsAndUpgradesLiveQuality(t *testing.T) {
	h := NewHistory(t.TempDir())
	at := time.Date(2026, 11, 14, 3, 0, 0, 0, time.UTC)
	v, accuracy := -90.0, 3.0
	for _, offset := range []time.Duration{0, 5 * time.Second, 6 * time.Second} {
		if err := h.Append(Observation{At: at.Add(offset), RSRP: &v, Source: "synthetic"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := h.Append(Observation{At: at, RSRP: &v, Source: "synthetic", GPS: &Position{At: at, Lat: 35, Lon: 139, Source: "stock-image-recognition"}}); err != nil {
		t.Fatal(err)
	}
	positions := []Position{{At: at, Lat: 35, Lon: 139, AccuracyM: &accuracy, Source: "stock-recording-nmea"}, {At: at.Add(6 * time.Second), Lat: 35, Lon: 140, AccuracyM: floatPtr(100), Source: "bad-accuracy"}}
	r, err := h.RepairPositions(positions, true)
	if err != nil || r.Updated != 3 || r.Unmatched != 1 {
		t.Fatal(r, err)
	}
	result, err := h.Query(Query{})
	if err != nil || len(result.Points) != 3 || result.Unplaced != 1 {
		t.Fatal(result, err)
	}
}

func floatPtr(v float64) *float64 { return &v }

func TestRepairMissingHistoryDoesNotCreateFiles(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "absent")
	_, err := NewHistory(dir).RepairPositions(nil, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("created storage")
	}
}

func TestRepairRetainsRotationThatGrowsBeyondTheAppendLimit(t *testing.T) {
	dir := t.TempDir()
	h := NewHistory(dir)
	at := time.Date(2026, 11, 14, 3, 0, 0, 0, time.UTC)
	line := []byte(`{"at":"2026-11-14T03:00:00Z","rsrp":-85,"source":"synthetic"}` + "\n")
	// Large malformed lines must also remain byte-for-byte intact.
	filler := append(bytes.Repeat([]byte{'x'}, 40000), '\n')
	raw := append([]byte(nil), line...)
	for len(raw) < maximumHistoryBytes {
		raw = append(raw, filler...)
	}
	raw = raw[:maximumHistoryBytes-10]
	filename := filepath.Join(dir, "radio-observations.jsonl.1")
	if err := os.WriteFile(filename, raw, 0600); err != nil {
		t.Fatal(err)
	}
	accuracy := 5.0
	r, err := h.RepairPositions([]Position{{At: at, Lat: 35, Lon: 139, AccuracyM: &accuracy, Source: "stock-recording-nmea"}}, true)
	if err != nil || r.Updated != 1 {
		t.Fatal(r, err)
	}
	updated, _ := os.ReadFile(filename)
	if len(updated) <= maximumHistoryBytes || !bytes.HasSuffix(updated, raw[len(line):]) {
		t.Fatal("rotation truncated or unchanged lines modified")
	}
	q, err := h.Query(Query{})
	if err != nil || len(q.Points) != 1 {
		t.Fatal("enlarged rotation became unreadable", q, err)
	}
}
