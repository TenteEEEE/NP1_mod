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

func stubRecorderAdvancedLock(t *testing.T) {
	t.Helper()
	original := recorderAdvancedLock
	recorderAdvancedLock = func() (func(), error) { return func() {}, nil }
	t.Cleanup(func() { recorderAdvancedLock = original })
}

func TestRecorderArtifactContractIsBuildLocalAndFailClosed(t *testing.T) {
	originalHook, originalInjector, originalOffset := recorderHookLibrarySHA, recorderHookInjectorSHA, recorderHookStatusOffText
	t.Cleanup(func() {
		recorderHookLibrarySHA, recorderHookInjectorSHA, recorderHookStatusOffText = originalHook, originalInjector, originalOffset
	})
	recorderHookLibrarySHA, recorderHookInjectorSHA, recorderHookStatusOffText = "", "", ""
	if RecorderArtifactContractReady() {
		t.Fatal("empty public build contract accepted")
	}
	recorderHookLibrarySHA = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	recorderHookInjectorSHA = "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"
	recorderHookStatusOffText = "0x365e0"
	if offset, ok := recorderArtifactContract(); !ok || offset != 0x365e0 {
		t.Fatalf("valid build-local contract rejected: offset=%#x ok=%v", offset, ok)
	}
	recorderHookLibrarySHA = "not-a-sha256"
	if RecorderArtifactContractReady() {
		t.Fatal("malformed artifact identity accepted")
	}
}

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
	stubRecorderAdvancedLock(t)
	directory := t.TempDir()
	originalPath, originalAppliedPath, originalAvailable := recorderAdvancedConfigPath, recorderAdvancedAppliedPath, recorderAdvancedAvailable
	originalRestart, originalHealthy, originalLoaded := recorderAdvancedRestart, recorderAdvancedHealthy, recorderAdvancedLoaded
	recorderAdvancedConfigPath = filepath.Join(directory, "recorder", "config")
	recorderAdvancedAppliedPath = filepath.Join(directory, "recorder", "applied-generation")
	recorderAdvancedAvailable = func() bool { return true }
	applies := 0
	recorderAdvancedRestart = func(context.Context) error { applies++; return nil }
	recorderAdvancedHealthy = func(context.Context) error { return nil }
	recorderAdvancedLoaded = func(context.Context) bool { return true }
	t.Cleanup(func() {
		recorderAdvancedConfigPath, recorderAdvancedAppliedPath, recorderAdvancedAvailable = originalPath, originalAppliedPath, originalAvailable
		recorderAdvancedRestart, recorderAdvancedHealthy, recorderAdvancedLoaded = originalRestart, originalHealthy, originalLoaded
	})
	settings := defaultRecorderAdvancedSettings()
	settings.FrontBitrateKbps = 8123
	body := map[string]any{"expectedGeneration": 0, "settings": settings, "applyNow": true, "confirmation": recorderApplyPhrase, "acknowledgeRecordingGap": true}
	raw, _ := json.Marshal(body)
	response := httptest.NewRecorder()
	HandleRecorderAdvanced(response, httptest.NewRequest("PUT", "/recorder-advanced", bytes.NewReader(raw)))
	if response.Code != 200 || applies != 1 {
		t.Fatalf("save/apply = %d applies=%d %s", response.Code, applies, response.Body.String())
	}
	var state RecorderAdvancedState
	if json.Unmarshal(response.Body.Bytes(), &state) != nil || !state.Applied || state.Generation != 1 || state.Settings.FrontBitrateKbps != 8123 {
		t.Fatalf("unexpected state %#v", state)
	}
	if readRecorderApplied(recorderAdvancedAppliedPath) != 1 {
		t.Fatal("generation was not marked applied after exact read-back")
	}
	response = httptest.NewRecorder()
	HandleRecorderAdvanced(response, httptest.NewRequest("PUT", "/recorder-advanced", bytes.NewReader(raw)))
	if response.Code != 409 || applies != 1 {
		t.Fatalf("stale generation escaped: %d %d", response.Code, applies)
	}
	body["expectedGeneration"] = 1
	body["confirmation"] = ""
	raw, _ = json.Marshal(body)
	response = httptest.NewRecorder()
	HandleRecorderAdvanced(response, httptest.NewRequest("PUT", "/recorder-advanced", bytes.NewReader(raw)))
	if response.Code != 400 || applies != 1 {
		t.Fatalf("missing confirmation escaped: %d %d", response.Code, applies)
	}
	recorderAdvancedRestart = func(context.Context) error { applies++; return errors.New("runtime apply unavailable") }
	body["confirmation"] = recorderApplyPhrase
	body["settings"] = func() RecorderAdvancedSettings { value := settings; value.CabinRecording = false; return value }()
	raw, _ = json.Marshal(body)
	response = httptest.NewRecorder()
	HandleRecorderAdvanced(response, httptest.NewRequest("PUT", "/recorder-advanced", bytes.NewReader(raw)))
	if response.Code != 202 || applies != 2 || readRecorderApplied(recorderAdvancedAppliedPath) != 0 {
		t.Fatalf("failed apply retained applied generation: %d %d", response.Code, applies)
	}
}

func TestEnsureRecorderHookAtBootRestartsOnceAndCommitsGeneration(t *testing.T) {
	stubRecorderAdvancedLock(t)
	directory := t.TempDir()
	originalPath, originalAppliedPath := recorderAdvancedConfigPath, recorderAdvancedAppliedPath
	originalAvailable, originalLoaded := recorderAdvancedAvailable, recorderAdvancedLoaded
	originalRestart, originalHealthy := recorderAdvancedRestart, recorderAdvancedHealthy
	recorderAdvancedConfigPath = filepath.Join(directory, "recorder", "config")
	recorderAdvancedAppliedPath = filepath.Join(directory, "recorder", "applied-generation")
	settings := defaultRecorderAdvancedSettings()
	if err := writeRecorderAdvanced(recorderAdvancedConfigPath, settings, 7); err != nil {
		t.Fatal(err)
	}
	recorderAdvancedAvailable = func() bool { return true }
	loaded, restarts := false, 0
	recorderAdvancedLoaded = func(context.Context) bool { return loaded }
	recorderAdvancedRestart = func(context.Context) error { restarts++; loaded = true; return nil }
	recorderAdvancedHealthy = func(context.Context) error { return nil }
	t.Cleanup(func() {
		recorderAdvancedConfigPath, recorderAdvancedAppliedPath = originalPath, originalAppliedPath
		recorderAdvancedAvailable, recorderAdvancedLoaded = originalAvailable, originalLoaded
		recorderAdvancedRestart, recorderAdvancedHealthy = originalRestart, originalHealthy
	})
	if err := EnsureRecorderHookAtBoot(context.Background()); err != nil {
		t.Fatal(err)
	}
	if restarts != 1 || readRecorderApplied(recorderAdvancedAppliedPath) != 7 {
		t.Fatalf("restarts=%d applied=%d", restarts, readRecorderApplied(recorderAdvancedAppliedPath))
	}
}

func TestEnsureRecorderHookAtBootDoesNotReapplyWhenAlreadyLoaded(t *testing.T) {
	stubRecorderAdvancedLock(t)
	originalAvailable, originalLoaded, originalRestart, originalHealthy := recorderAdvancedAvailable, recorderAdvancedLoaded, recorderAdvancedRestart, recorderAdvancedHealthy
	recorderAdvancedAvailable = func() bool { return true }
	recorderAdvancedLoaded = func(context.Context) bool { return true }
	recorderAdvancedHealthy = func(context.Context) error { return nil }
	restarts := 0
	recorderAdvancedRestart = func(context.Context) error { restarts++; return nil }
	t.Cleanup(func() {
		recorderAdvancedAvailable, recorderAdvancedLoaded, recorderAdvancedRestart, recorderAdvancedHealthy = originalAvailable, originalLoaded, originalRestart, originalHealthy
	})
	if err := EnsureRecorderHookAtBoot(context.Background()); err != nil || restarts != 0 {
		t.Fatalf("loaded reconciliation = %v, restarts=%d", err, restarts)
	}
}

func TestRecorderAdvancedDeferredSaveWaitsForNextDriveRecProcess(t *testing.T) {
	stubRecorderAdvancedLock(t)
	directory := t.TempDir()
	originalPath, originalAppliedPath, originalDeferredPath := recorderAdvancedConfigPath, recorderAdvancedAppliedPath, recorderAdvancedDeferredPath
	originalAvailable, originalLoaded := recorderAdvancedAvailable, recorderAdvancedLoaded
	originalRestart, originalHealthy, originalIdentity := recorderAdvancedRestart, recorderAdvancedHealthy, recorderAdvancedIdentity
	recorderAdvancedConfigPath = filepath.Join(directory, "recorder", "config")
	recorderAdvancedAppliedPath = filepath.Join(directory, "recorder", "applied-generation")
	recorderAdvancedDeferredPath = filepath.Join(directory, "recorder", "deferred-until-restart")
	recorderAdvancedAvailable = func() bool { return true }
	identity := recorderIdentity{PID: 123, StartTime: 456}
	recorderAdvancedIdentity = func() (recorderIdentity, error) { return identity, nil }
	loaded, restarts := true, 0
	recorderAdvancedLoaded = func(context.Context) bool { return loaded }
	recorderAdvancedRestart = func(context.Context) error { restarts++; loaded = true; return nil }
	recorderAdvancedHealthy = func(context.Context) error { return nil }
	t.Cleanup(func() {
		recorderAdvancedConfigPath, recorderAdvancedAppliedPath, recorderAdvancedDeferredPath = originalPath, originalAppliedPath, originalDeferredPath
		recorderAdvancedAvailable, recorderAdvancedLoaded = originalAvailable, originalLoaded
		recorderAdvancedRestart, recorderAdvancedHealthy, recorderAdvancedIdentity = originalRestart, originalHealthy, originalIdentity
	})
	settings := defaultRecorderAdvancedSettings()
	settings.FrontBitrateKbps = 2345
	body := map[string]any{"expectedGeneration": 0, "settings": settings, "applyNow": false, "confirmation": "", "acknowledgeRecordingGap": false}
	raw, _ := json.Marshal(body)
	response := httptest.NewRecorder()
	HandleRecorderAdvanced(response, httptest.NewRequest("PUT", "/recorder-advanced", bytes.NewReader(raw)))
	if response.Code != 200 || restarts != 0 || readRecorderApplied(recorderAdvancedAppliedPath) != 0 {
		t.Fatalf("deferred save = %d restarts=%d applied=%d %s", response.Code, restarts, readRecorderApplied(recorderAdvancedAppliedPath), response.Body.String())
	}
	if generation, savedIdentity := readRecorderDeferred(recorderAdvancedDeferredPath); generation != 1 || savedIdentity != identity {
		t.Fatalf("deferred marker = %d %#v", generation, savedIdentity)
	}
	if err := EnsureRecorderHookAtBoot(context.Background()); err != nil || restarts != 0 {
		t.Fatalf("same process applied deferred settings: %v restarts=%d", err, restarts)
	}
	identity.StartTime++
	loaded = false
	if err := EnsureRecorderHookAtBoot(context.Background()); err != nil || restarts != 1 || readRecorderApplied(recorderAdvancedAppliedPath) != 1 {
		t.Fatalf("next process did not apply settings: %v restarts=%d applied=%d", err, restarts, readRecorderApplied(recorderAdvancedAppliedPath))
	}
	if generation, _ := readRecorderDeferred(recorderAdvancedDeferredPath); generation != 0 {
		t.Fatalf("deferred marker survived application: %d", generation)
	}
}

func TestEnsureRecorderHookAtBootInjectsBeforeStockHealthCheck(t *testing.T) {
	stubRecorderAdvancedLock(t)
	originalAvailable, originalLoaded, originalRestart, originalHealthy := recorderAdvancedAvailable, recorderAdvancedLoaded, recorderAdvancedRestart, recorderAdvancedHealthy
	recorderAdvancedAvailable = func() bool { return true }
	recorderAdvancedLoaded = func(context.Context) bool { return false }
	restarts := 0
	recorderAdvancedRestart = func(context.Context) error {
		restarts++
		recorderAdvancedLoaded = func(context.Context) bool { return true }
		return nil
	}
	recorderAdvancedHealthy = func(context.Context) error { return errors.New("stock encoders are still starting") }
	t.Cleanup(func() {
		recorderAdvancedAvailable, recorderAdvancedLoaded, recorderAdvancedRestart, recorderAdvancedHealthy = originalAvailable, originalLoaded, originalRestart, originalHealthy
	})
	if err := EnsureRecorderHookAtBoot(context.Background()); err == nil || restarts != 1 {
		t.Fatalf("startup injection did not precede health check: err=%v restarts=%d", err, restarts)
	}
}

func TestEnsureRecorderHookAtBootDoesNotRaceUserApply(t *testing.T) {
	recorderAdvancedMu.Lock()
	defer recorderAdvancedMu.Unlock()
	if err := EnsureRecorderHookAtBoot(context.Background()); err == nil {
		t.Fatal("runtime reconciliation entered while user apply lock was held")
	}
}
