package api

import (
	"context"
	"encoding/json"
	"github.com/np1-control-center/ncc/internal/config"
	"github.com/np1-control-center/ncc/internal/radio"
	"github.com/np1-control-center/ncc/internal/trips"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoggerEnabledWithoutConsentAndPersistsUniqueMeasuredSamples(t *testing.T) {
	directory := t.TempDir()
	server := &Server{cfg: config.Config{StateDir: directory}, radioStore: radio.NewHistory(directory)}
	server.loadLogger()
	if !server.logger.Enabled {
		t.Fatal("owner logging instruction not applied")
	}
	at := time.Now().UTC()
	rsrp := float64(-85)
	sample := radio.Observation{At: at, Source: "synthetic-unit-measurement", RSRP: &rsrp}
	if e := server.storeRadio(sample, at); e != nil {
		t.Fatal(e)
	}
	if e := server.storeRadio(sample, at.Add(time.Second)); e != nil {
		t.Fatal(e)
	}
	if server.logger.Stored != 1 || server.logger.Located != 0 {
		t.Fatal(server.logger)
	}
	if _, e := os.Stat(filepath.Join(directory, "radio-observations.jsonl")); e != nil {
		t.Fatal(e)
	}
	reloaded := &Server{cfg: config.Config{StateDir: directory}, radioStore: radio.NewHistory(directory)}
	reloaded.loadLogger()
	if e := reloaded.storeRadio(sample, at.Add(time.Second)); e != nil {
		t.Fatal(e)
	}
	if reloaded.logger.Stored != 1 {
		t.Fatal("sample duplicated after restart")
	}
	sample.At = at.Add(-3 * time.Minute)
	_ = reloaded.storeRadio(sample, at)
	sample.At = at.Add(3 * time.Minute)
	_ = reloaded.storeRadio(sample, at)
	if reloaded.logger.Stored != 1 {
		t.Fatal("invalid measurement time logged")
	}
}

func TestRadioCollectorRepairsAfterRecordingCloses(t *testing.T) {
	directory := t.TempDir()
	now := time.Now().UTC()
	at := now.Add(-3 * time.Minute)
	server := &Server{cfg: config.Config{StateDir: directory}, radioStore: radio.NewHistory(directory)}
	v, a := -85.0, 4.0
	if err := server.storeRadio(radio.Observation{At: at, RSRP: &v, Source: "synthetic-radio"}, at); err != nil {
		t.Fatal(err)
	}
	server.client = &http.Client{Transport: journeyRoundTrip(func(r *http.Request) (*http.Response, error) {
		if r.Method != "GET" || r.URL.Path != "/recordings/track" {
			t.Fatal(r.Method, r.URL.Path)
		}
		from, _ := time.Parse(time.RFC3339Nano, r.URL.Query().Get("from"))
		to, _ := time.Parse(time.RFC3339Nano, r.URL.Query().Get("to"))
		if to.Sub(from) != 15*time.Minute {
			t.Fatal("unexpected repair scope")
		}
		body, _ := json.Marshal(map[string]any{"data": trips.TrackReport{Source: "stock-recording-nmea", Points: []trips.TrackPoint{{At: at, Lat: 35, Lon: 139, AccuracyM: &a}}}})
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(body)))}, nil
	})}
	server.repairRecentRadio(context.Background(), now)
	result, err := server.radioStore.Query(radio.Query{})
	if err != nil || len(result.Points) != 1 || server.logger.Repaired != 1 || server.logger.RepairError != "" {
		t.Fatal(result, server.logger, err)
	}
	server.repairRecentRadio(context.Background(), now)
	if server.logger.Repaired != 1 {
		t.Fatal("repair duplicated after rescan")
	}
}
