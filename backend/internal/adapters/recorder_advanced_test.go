package adapters

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"hash/crc32"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

func TestRecorderAdvancedBinaryRoundTripAndCorruption(t *testing.T) {
	settings := RecorderAdvancedSettings{FrontBitrateKbps: 9876, CabinBitrateKbps: 654, FrontRateControl: "cbr", CabinRateControl: "vbr", FrontGOPMillis: 750, CabinGOPMillis: 2250, CabinRecording: false, SegmentSeconds: 240}
	data, err := encodeRecorderAdvanced(settings, 19)
	if err != nil || len(data) != recorderConfigSize {
		t.Fatal(err, len(data))
	}
	got, generation, err := decodeRecorderAdvanced(data)
	if err != nil || got != settings || generation != 19 {
		t.Fatalf("round trip = %#v %d %v", got, generation, err)
	}
	data[20] ^= 1
	if _, _, err = decodeRecorderAdvanced(data); err == nil {
		t.Fatal("corrupt config accepted")
	}
}

func TestRecorderAdvancedDecodesLegacyReservedSegmentAsStockDefault(t *testing.T) {
	settings := defaultRecorderAdvancedSettings()
	data, err := encodeRecorderAdvanced(settings, 3)
	if err != nil {
		t.Fatal(err)
	}
	binary.LittleEndian.PutUint32(data[44:], 0)
	binary.LittleEndian.PutUint32(data[60:], crc32.ChecksumIEEE(data[:60]))
	got, _, err := decodeRecorderAdvanced(data)
	if err != nil || got.SegmentSeconds != 180 {
		t.Fatalf("legacy segment = %d, %v", got.SegmentSeconds, err)
	}
}

func TestRecorderAdvancedHandlerRequiresGenerationAndRestartConfirmation(t *testing.T) {
	directory := t.TempDir()
	originalPath, originalAppliedPath, originalAvailable, originalLoaded, originalRestart := recorderAdvancedConfigPath, recorderAdvancedAppliedPath, recorderAdvancedAvailable, recorderAdvancedLoaded, recorderAdvancedRestart
	recorderAdvancedConfigPath = filepath.Join(directory, "recorder", "config")
	recorderAdvancedAppliedPath = filepath.Join(directory, "recorder", "applied-generation")
	recorderAdvancedAvailable = func() bool { return true }
	recorderAdvancedLoaded = func(context.Context) bool { return true }
	restarts := 0
	recorderAdvancedRestart = func(context.Context) error { restarts++; return nil }
	t.Cleanup(func() {
		recorderAdvancedConfigPath, recorderAdvancedAppliedPath, recorderAdvancedAvailable, recorderAdvancedLoaded, recorderAdvancedRestart = originalPath, originalAppliedPath, originalAvailable, originalLoaded, originalRestart
	})
	settings := defaultRecorderAdvancedSettings()
	settings.FrontBitrateKbps = 8123
	body := map[string]any{"expectedGeneration": 0, "settings": settings, "applyNow": true, "confirmation": recorderApplyPhrase, "acknowledgeRecordingGap": true}
	raw, _ := json.Marshal(body)
	response := httptest.NewRecorder()
	HandleRecorderAdvanced(response, httptest.NewRequest("PUT", "/recorder-advanced", bytes.NewReader(raw)))
	if response.Code != 200 || restarts != 1 {
		t.Fatalf("save/apply = %d restarts=%d %s", response.Code, restarts, response.Body.String())
	}
	var state RecorderAdvancedState
	if json.Unmarshal(response.Body.Bytes(), &state) != nil || !state.Applied || state.Generation != 1 || state.Settings.FrontBitrateKbps != 8123 {
		t.Fatalf("unexpected state %#v", state)
	}
	if readRecorderApplied(recorderAdvancedAppliedPath) != 1 {
		t.Fatal("applied generation was not committed after read-back")
	}
	response = httptest.NewRecorder()
	HandleRecorderAdvanced(response, httptest.NewRequest("PUT", "/recorder-advanced", bytes.NewReader(raw)))
	if response.Code != 409 || restarts != 1 {
		t.Fatalf("stale generation escaped: %d %d", response.Code, restarts)
	}
	body["expectedGeneration"] = 1
	body["confirmation"] = ""
	raw, _ = json.Marshal(body)
	response = httptest.NewRecorder()
	HandleRecorderAdvanced(response, httptest.NewRequest("PUT", "/recorder-advanced", bytes.NewReader(raw)))
	if response.Code != 400 || restarts != 1 {
		t.Fatalf("missing confirmation escaped: %d %d", response.Code, restarts)
	}
	recorderAdvancedRestart = func(context.Context) error { restarts++; return errors.New("restart unavailable") }
	body["confirmation"] = recorderApplyPhrase
	body["settings"] = func() RecorderAdvancedSettings { value := settings; value.CabinRecording = false; return value }()
	raw, _ = json.Marshal(body)
	response = httptest.NewRecorder()
	HandleRecorderAdvanced(response, httptest.NewRequest("PUT", "/recorder-advanced", bytes.NewReader(raw)))
	if response.Code != 202 || restarts != 2 || readRecorderApplied(recorderAdvancedAppliedPath) != 0 {
		t.Fatalf("failed restart retained applied generation: %d %d", response.Code, restarts)
	}
}
