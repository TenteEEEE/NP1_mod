package radio

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/np1-control-center/ncc/internal/trips"
)

func TestMeasuredTrackImportKeepsHDOPWithoutInventingMetreAccuracy(t *testing.T) {
	at := time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC)
	hdop := 1.4
	track := trips.TrackReport{Date: "2026-11-15", Source: "stock-recording-nmea", Points: []trips.TrackPoint{{At: at, Lat: 35, Lon: 139, HDOP: &hdop}}}
	raw, _ := json.Marshal(map[string]any{"status": "available", "data": track})
	positions, partial, err := ReadRecordedPositions(strings.NewReader(string(raw)), at.Add(time.Hour))
	if err != nil || partial || len(positions) != 1 || positions[0].AccuracyM != nil || positions[0].HDOP == nil || *positions[0].HDOP != hdop {
		t.Fatal(positions, partial, err)
	}
	history := NewHistory(t.TempDir())
	value := -85.0
	if err = history.Append(Observation{At: at.Add(time.Second), RSRP: &value, Source: "synthetic-radio"}); err != nil {
		t.Fatal(err)
	}
	report, err := history.RepairPositions(positions, true)
	if err != nil || report.Updated != 1 {
		t.Fatal(report, err)
	}
}

func TestMeasuredTrackImportRejectsUnconfirmedSourceAndInvalidTimes(t *testing.T) {
	at := time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC)
	for _, kind := range []string{"source", "status", "future", "other-day", "coordinate"} {
		t.Run(kind, func(t *testing.T) {
			track := trips.TrackReport{Date: "2026-11-15", Source: "stock-recording-nmea", Points: []trips.TrackPoint{{At: at, Lat: 35, Lon: 139}}}
			status := "available"
			switch kind {
			case "source":
				track.Source = "estimated"
			case "status":
				status = "unknown"
			case "future":
				track.Points[0].At = at.Add(time.Hour)
			case "other-day":
				track.Date = "2026-11-14"
			case "coordinate":
				track.Points[0].Lat = 999
			}
			raw, _ := json.Marshal(map[string]any{"status": status, "data": track})
			if _, _, err := ReadRecordedPositions(strings.NewReader(string(raw)), at); err == nil {
				t.Fatal("invalid measured source accepted")
			}
		})
	}
}
