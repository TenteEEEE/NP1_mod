package adapters

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	recorderHookSource      = "/overlay/owner-apps/np1-control/recorder-hook/libncc-driverec-hook.so"
	recorderHookLibrary     = "/run/np1-control-recorder-hook/libncc-driverec-hook.so"
	recorderHookConfig      = "/overlay/owner-apps/np1-control/recorder-hook/config"
	recorderHookApplied     = "/overlay/owner-apps/np1-control/recorder-hook/applied-generation"
	recorderHookDeferred    = "/overlay/owner-apps/np1-control/recorder-hook/deferred-until-restart"
	recorderHookLibrarySHA  = "29e2984c16ad7501d3b8bfac445fa2dd96ae70749d4b396dc1403c1dd78c5bf6"
	recorderHookStatusOff   = 0x365e0
	recorderHookInjector    = "/run/np1-control-native/recorder-injector"
	recorderHookInjectorSHA = "c79616d51adee1560d35dd5cf2a54d4fb3ed28ee616c316667ca7b8eaa280d29"
	recorderDriveRecSHA     = "000871dc36581f27ae831d9db3772b553a17cce0d814c52867276ac68ea0cde0"
	recorderConfigSize      = 64
	recorderConfigVersion   = 1
	recorderApplyPhrase     = "即時適用する"
	recorderMinimumBitrate  = 128
	recorderMaximumBitrate  = 50000
	recorderMinimumGOP      = 250
	recorderMaximumGOP      = 10000
	recorderMinimumSegment  = 30
	recorderMaximumSegment  = 600
)

type RecorderAdvancedSettings struct {
	FrontBitrateKbps uint32 `json:"frontBitrateKbps"`
	CabinBitrateKbps uint32 `json:"cabinBitrateKbps"`
	FrontRateControl string `json:"frontRateControl"`
	CabinRateControl string `json:"cabinRateControl"`
	FrontGOPMillis   uint32 `json:"frontGopMilliseconds"`
	CabinGOPMillis   uint32 `json:"cabinGopMilliseconds"`
	CabinRecording   bool   `json:"cabinRecording"`
	SegmentSeconds   uint32 `json:"segmentDurationSeconds"`
}

type RecorderAdvancedState struct {
	Available       bool                     `json:"available"`
	Configured      bool                     `json:"configured"`
	HookLoaded      bool                     `json:"hookLoaded"`
	RestartRequired bool                     `json:"restartRequired"`
	Generation      uint32                   `json:"generation"`
	Settings        RecorderAdvancedSettings `json:"settings"`
	ObservedAt      time.Time                `json:"observedAt"`
	Applied         bool                     `json:"applied,omitempty"`
	Message         string                   `json:"message,omitempty"`
}

var recorderAdvancedMu sync.Mutex
var recorderAdvancedConfigPath = recorderHookConfig
var recorderAdvancedAppliedPath = recorderHookApplied
var recorderAdvancedDeferredPath = recorderHookDeferred
var recorderAdvancedAvailable = func() bool { return nativeFirmwareMatches(recorderHookSource, recorderHookLibrarySHA) }
var recorderAdvancedLoaded = recorderHookLoaded
var recorderAdvancedRestart = restartDriveRecForRecorderHook
var recorderAdvancedHealthy = verifyRecorderFunction
var recorderAdvancedIdentity = recorderDriveRecIdentity

func defaultRecorderAdvancedSettings() RecorderAdvancedSettings {
	return RecorderAdvancedSettings{FrontBitrateKbps: 10000, CabinBitrateKbps: 4000, FrontRateControl: "vbr", CabinRateControl: "vbr", FrontGOPMillis: 1000, CabinGOPMillis: 1000, CabinRecording: true, SegmentSeconds: 180}
}

func recorderRateValue(value string) (uint32, bool) {
	switch value {
	case "cbr":
		return 1, true
	case "vbr":
		return 2, true
	default:
		return 0, false
	}
}

func validRecorderAdvanced(settings RecorderAdvancedSettings) bool {
	_, frontRate := recorderRateValue(settings.FrontRateControl)
	_, cabinRate := recorderRateValue(settings.CabinRateControl)
	return frontRate && cabinRate && settings.FrontBitrateKbps >= recorderMinimumBitrate && settings.FrontBitrateKbps <= recorderMaximumBitrate &&
		settings.CabinBitrateKbps >= recorderMinimumBitrate && settings.CabinBitrateKbps <= recorderMaximumBitrate &&
		settings.FrontGOPMillis >= recorderMinimumGOP && settings.FrontGOPMillis <= recorderMaximumGOP && settings.CabinGOPMillis >= recorderMinimumGOP && settings.CabinGOPMillis <= recorderMaximumGOP &&
		settings.SegmentSeconds >= recorderMinimumSegment && settings.SegmentSeconds <= recorderMaximumSegment
}

func encodeRecorderAdvanced(settings RecorderAdvancedSettings, generation uint32) ([]byte, error) {
	if !validRecorderAdvanced(settings) || generation == 0 {
		return nil, errors.New("invalid recorder hook settings")
	}
	frontRate, _ := recorderRateValue(settings.FrontRateControl)
	cabinRate, _ := recorderRateValue(settings.CabinRateControl)
	data := make([]byte, recorderConfigSize)
	copy(data, []byte{'N', 'C', 'C', 'R', 'E', 'C', '1', 0})
	binary.LittleEndian.PutUint32(data[8:], recorderConfigVersion)
	binary.LittleEndian.PutUint32(data[12:], recorderConfigSize)
	binary.LittleEndian.PutUint32(data[16:], settings.FrontBitrateKbps*1000)
	binary.LittleEndian.PutUint32(data[20:], settings.CabinBitrateKbps*1000)
	binary.LittleEndian.PutUint32(data[24:], frontRate)
	binary.LittleEndian.PutUint32(data[28:], cabinRate)
	binary.LittleEndian.PutUint32(data[32:], settings.FrontGOPMillis)
	binary.LittleEndian.PutUint32(data[36:], settings.CabinGOPMillis)
	if settings.CabinRecording {
		binary.LittleEndian.PutUint32(data[40:], 1)
	}
	binary.LittleEndian.PutUint32(data[44:], settings.SegmentSeconds)
	binary.LittleEndian.PutUint32(data[56:], generation)
	binary.LittleEndian.PutUint32(data[60:], crc32.ChecksumIEEE(data[:60]))
	return data, nil
}

func decodeRecorderAdvanced(data []byte) (RecorderAdvancedSettings, uint32, error) {
	if len(data) != recorderConfigSize || string(data[:8]) != "NCCREC1\x00" || binary.LittleEndian.Uint32(data[8:]) != recorderConfigVersion || binary.LittleEndian.Uint32(data[12:]) != recorderConfigSize || crc32.ChecksumIEEE(data[:60]) != binary.LittleEndian.Uint32(data[60:]) {
		return RecorderAdvancedSettings{}, 0, errors.New("invalid recorder hook config")
	}
	for _, offset := range []int{48, 52} {
		if binary.LittleEndian.Uint32(data[offset:]) != 0 {
			return RecorderAdvancedSettings{}, 0, errors.New("unsupported recorder hook config")
		}
	}
	rateName := func(value uint32) (string, bool) {
		switch value {
		case 1:
			return "cbr", true
		case 2:
			return "vbr", true
		}
		return "", false
	}
	frontRate, frontOK := rateName(binary.LittleEndian.Uint32(data[24:]))
	cabinRate, cabinOK := rateName(binary.LittleEndian.Uint32(data[28:]))
	frontBPS, cabinBPS := binary.LittleEndian.Uint32(data[16:]), binary.LittleEndian.Uint32(data[20:])
	cabinEnabled := binary.LittleEndian.Uint32(data[40:])
	generation := binary.LittleEndian.Uint32(data[56:])
	segmentSeconds := binary.LittleEndian.Uint32(data[44:])
	if segmentSeconds == 0 {
		segmentSeconds = 180
	}
	settings := RecorderAdvancedSettings{FrontBitrateKbps: frontBPS / 1000, CabinBitrateKbps: cabinBPS / 1000, FrontRateControl: frontRate, CabinRateControl: cabinRate, FrontGOPMillis: binary.LittleEndian.Uint32(data[32:]), CabinGOPMillis: binary.LittleEndian.Uint32(data[36:]), CabinRecording: cabinEnabled == 1, SegmentSeconds: segmentSeconds}
	if !frontOK || !cabinOK || frontBPS%1000 != 0 || cabinBPS%1000 != 0 || cabinEnabled > 1 || generation == 0 || !validRecorderAdvanced(settings) {
		return RecorderAdvancedSettings{}, 0, errors.New("unsupported recorder hook values")
	}
	return settings, generation, nil
}

func readRecorderAdvanced(path string) (RecorderAdvancedSettings, uint32, bool, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return defaultRecorderAdvancedSettings(), 0, false, nil
	}
	if err != nil {
		return RecorderAdvancedSettings{}, 0, false, err
	}
	settings, generation, err := decodeRecorderAdvanced(data)
	return settings, generation, true, err
}

func writeRecorderFile(path string, data []byte) error {
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0755); err != nil {
		return err
	}
	if info, statErr := os.Lstat(directory); statErr != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("recorder hook directory identity mismatch")
	}
	temporary, err := os.CreateTemp(directory, ".recorder-*")
	if err != nil {
		return err
	}
	temporaryName := temporary.Name()
	defer os.Remove(temporaryName)
	if err = temporary.Chmod(0644); err == nil {
		_, err = temporary.Write(data)
	}
	if err == nil {
		err = temporary.Sync()
	}
	closeErr := temporary.Close()
	if err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(temporaryName, path)
	}
	if err != nil {
		return err
	}
	dir, err := os.Open(directory)
	if err != nil {
		return err
	}
	err = dir.Sync()
	closeErr = dir.Close()
	if runtime.GOOS == "windows" {
		err = nil
	}
	if err != nil {
		return err
	}
	return closeErr
}

func writeRecorderAdvanced(path string, settings RecorderAdvancedSettings, generation uint32) error {
	data, err := encodeRecorderAdvanced(settings, generation)
	if err != nil {
		return err
	}
	return writeRecorderFile(path, data)
}

func readRecorderApplied(path string) uint32 {
	data, err := os.ReadFile(path)
	if err != nil || len(data) != 16 || string(data[:8]) != "NCCAPL1\x00" || crc32.ChecksumIEEE(data[:12]) != binary.LittleEndian.Uint32(data[12:]) {
		return 0
	}
	return binary.LittleEndian.Uint32(data[8:])
}

func writeRecorderApplied(path string, generation uint32) error {
	if generation == 0 {
		return errors.New("invalid applied recorder generation")
	}
	data := make([]byte, 16)
	copy(data, []byte{'N', 'C', 'C', 'A', 'P', 'L', '1', 0})
	binary.LittleEndian.PutUint32(data[8:], generation)
	binary.LittleEndian.PutUint32(data[12:], crc32.ChecksumIEEE(data[:12]))
	return writeRecorderFile(path, data)
}

type recorderIdentity struct {
	PID       uint32
	StartTime uint64
}

func recorderDriveRecIdentity() (recorderIdentity, error) {
	text, err := command("systemctl", "show", "app-@DriveRec.service", "-p", "MainPID", "-p", "ActiveState", "-p", "SubState", "-p", "Job")
	if err != nil {
		return recorderIdentity{}, err
	}
	properties := map[string]string{}
	for _, line := range strings.Split(text, "\n") {
		if pair := strings.SplitN(line, "=", 2); len(pair) == 2 {
			properties[pair[0]] = pair[1]
		}
	}
	pid64, err := strconv.ParseUint(properties["MainPID"], 10, 31)
	if err != nil || pid64 <= 1 || properties["ActiveState"] != "active" || properties["SubState"] != "running" || properties["Job"] != "" {
		return recorderIdentity{}, errors.New("DriveRec process identity unavailable")
	}
	stat, err := os.ReadFile("/proc/" + strconv.FormatUint(pid64, 10) + "/stat")
	if err != nil {
		return recorderIdentity{}, err
	}
	closing := strings.LastIndex(string(stat), ") ")
	if closing < 0 {
		return recorderIdentity{}, errors.New("DriveRec process start time unavailable")
	}
	fields := strings.Fields(string(stat)[closing+2:])
	if len(fields) <= 19 {
		return recorderIdentity{}, errors.New("DriveRec process stat is incomplete")
	}
	start, err := strconv.ParseUint(fields[19], 10, 64)
	if err != nil || start == 0 {
		return recorderIdentity{}, errors.New("DriveRec process start time is invalid")
	}
	return recorderIdentity{PID: uint32(pid64), StartTime: start}, nil
}

func writeRecorderDeferred(path string, generation uint32, identity recorderIdentity) error {
	if generation == 0 || identity.PID <= 1 || identity.StartTime == 0 {
		return errors.New("invalid deferred recorder generation")
	}
	data := make([]byte, 28)
	copy(data, []byte{'N', 'C', 'C', 'D', 'E', 'F', '1', 0})
	binary.LittleEndian.PutUint32(data[8:], generation)
	binary.LittleEndian.PutUint32(data[12:], identity.PID)
	binary.LittleEndian.PutUint64(data[16:], identity.StartTime)
	binary.LittleEndian.PutUint32(data[24:], crc32.ChecksumIEEE(data[:24]))
	return writeRecorderFile(path, data)
}

func readRecorderDeferred(path string) (uint32, recorderIdentity) {
	data, err := os.ReadFile(path)
	if err != nil || len(data) != 28 || string(data[:8]) != "NCCDEF1\x00" || crc32.ChecksumIEEE(data[:24]) != binary.LittleEndian.Uint32(data[24:]) {
		return 0, recorderIdentity{}
	}
	return binary.LittleEndian.Uint32(data[8:]), recorderIdentity{PID: binary.LittleEndian.Uint32(data[12:]), StartTime: binary.LittleEndian.Uint64(data[16:])}
}

func recorderDeferredForCurrent(generation uint32) bool {
	deferredGeneration, deferredIdentity := readRecorderDeferred(recorderAdvancedDeferredPath)
	if deferredGeneration != generation {
		return false
	}
	current, err := recorderAdvancedIdentity()
	return err == nil && current == deferredIdentity
}

func recorderHookLoaded(ctx context.Context) bool {
	if !nativeFirmwareMatches(recorderHookLibrary, recorderHookLibrarySHA) {
		log.Printf("recorder-hook readback failed: runtime library identity mismatch")
		return false
	}
	text, err := command("systemctl", "show", "app-@DriveRec.service", "-p", "MainPID", "-p", "ActiveState", "-p", "SubState", "-p", "Job")
	properties := make(map[string]string)
	for _, line := range strings.Split(text, "\n") {
		if pair := strings.SplitN(line, "=", 2); len(pair) == 2 {
			properties[pair[0]] = pair[1]
		}
	}
	if err != nil || properties["ActiveState"] != "active" || properties["SubState"] != "running" || properties["Job"] != "" {
		log.Printf("recorder-hook readback failed: unstable service state")
		return false
	}
	pid, err := strconv.ParseUint(properties["MainPID"], 10, 31)
	if err != nil || pid <= 1 {
		log.Printf("recorder-hook readback failed: invalid service pid")
		return false
	}
	proc := "/proc/" + strconv.FormatUint(pid, 10)
	if !nativeFirmwareMatches(proc+"/exe", recorderDriveRecSHA) {
		log.Printf("recorder-hook readback failed: executable identity mismatch")
		return false
	}
	maps, err := os.ReadFile(proc + "/maps")
	if err != nil {
		log.Printf("recorder-hook readback failed: process maps unavailable")
		return false
	}
	var imageBase, multimediaBase, hookBase, hookStart, hookEnd uint64
	for _, line := range strings.Split(string(maps), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 6 {
			continue
		}
		addresses := strings.SplitN(fields[0], "-", 2)
		if len(addresses) != 2 {
			continue
		}
		start, startErr := strconv.ParseUint(addresses[0], 16, 64)
		end, endErr := strconv.ParseUint(addresses[1], 16, 64)
		offset, offsetErr := strconv.ParseUint(fields[2], 16, 64)
		if startErr != nil || endErr != nil || offsetErr != nil {
			continue
		}
		if fields[5] == "/usr/local/bin/DriveRec" && offset == 0 {
			imageBase = start
		}
		if strings.HasSuffix(fields[5], "/libNPMultimediaPlayerStack.so") && offset == 0 {
			multimediaBase = start
		}
		if fields[5] == recorderHookLibrary && strings.Contains(fields[1], "x") {
			hookStart, hookEnd = start, end
		}
		if fields[5] == recorderHookLibrary && offset == 0 {
			hookBase = start
		}
	}
	if imageBase == 0 || multimediaBase == 0 || hookBase == 0 || hookStart == 0 || hookEnd <= hookStart {
		log.Printf("recorder-hook readback failed: executable mapping unavailable")
		return false
	}
	memory, err := os.Open(proc + "/mem")
	if err != nil {
		log.Printf("recorder-hook readback failed: process memory unavailable")
		return false
	}
	defer memory.Close()
	settings, generation, _, err := readRecorderAdvanced(recorderAdvancedConfigPath)
	if err != nil {
		log.Printf("recorder-hook readback failed: config unavailable")
		return false
	}
	var durationInstruction [4]byte
	expectedDurationInstruction := uint32(0x52800006) | settings.SegmentSeconds<<5
	if _, err = memory.ReadAt(durationInstruction[:], int64(imageBase+0x14c774)); err != nil || binary.LittleEndian.Uint32(durationInstruction[:]) != expectedDurationInstruction {
		log.Printf("recorder-hook readback failed: segment duration mismatch")
		return false
	}
	for _, offset := range []uint64{0x138378, 0x1384d8, 0x138658} {
		var jump [16]byte
		if _, err = memory.ReadAt(jump[:], int64(imageBase+offset)); err != nil || binary.LittleEndian.Uint32(jump[0:4]) != 0x58000050 || binary.LittleEndian.Uint32(jump[4:8]) != 0xd61f0200 {
			log.Printf("recorder-hook readback failed: jump mismatch at %#x", offset)
			return false
		}
		target := binary.LittleEndian.Uint64(jump[8:16])
		if target < hookStart || target >= hookEnd {
			log.Printf("recorder-hook readback failed: jump target mismatch at %#x", offset)
			return false
		}
	}
	var streamerGOT [8]byte
	if _, err = memory.ReadAt(streamerGOT[:], int64(imageBase+0x22cdb0)); err != nil {
		log.Printf("recorder-hook readback failed: streamer GOT unavailable")
		return false
	}
	streamerTarget := binary.LittleEndian.Uint64(streamerGOT[:])
	if streamerTarget < hookStart || streamerTarget >= hookEnd {
		log.Printf("recorder-hook readback failed: streamer GOT target mismatch")
		return false
	}
	// The pinned MPS_StreamBuffer vtable has write() at symbol+40
	// (image offset 0x1970a0). It must point into the executable hook mapping;
	// a loaded .so without this process-memory patch is not sufficient proof.
	var streamWrite [8]byte
	if _, err = memory.ReadAt(streamWrite[:], int64(multimediaBase+0x1970a0)); err != nil {
		log.Printf("recorder-hook readback failed: stream write vtable unavailable")
		return false
	}
	streamWriteTarget := binary.LittleEndian.Uint64(streamWrite[:])
	if streamWriteTarget < hookStart || streamWriteTarget >= hookEnd {
		log.Printf("recorder-hook readback failed: stream write hook target mismatch")
		return false
	}
	var applied [52]byte
	if _, err = memory.ReadAt(applied[:], int64(hookBase+recorderHookStatusOff)); err != nil || binary.LittleEndian.Uint64(applied[:8]) != 0x314445494c505041 || binary.LittleEndian.Uint32(applied[8:12]) != generation || binary.LittleEndian.Uint32(applied[12:16]) != 3 {
		log.Printf("recorder-hook readback failed: applied encoder generation mismatch")
		return false
	}
	frontRate, _ := recorderRateValue(settings.FrontRateControl)
	cabinRate, _ := recorderRateValue(settings.CabinRateControl)
	expected := []uint32{settings.FrontBitrateKbps * 1000, frontRate, settings.FrontGOPMillis, settings.CabinBitrateKbps * 1000, cabinRate, settings.CabinGOPMillis}
	for i, value := range expected {
		if binary.LittleEndian.Uint32(applied[16+i*4:20+i*4]) != value {
			log.Printf("recorder-hook readback failed: applied encoder value mismatch")
			return false
		}
	}
	if binary.LittleEndian.Uint32(applied[40:44]) != 2 || binary.LittleEndian.Uint32(applied[44:48]) != settings.SegmentSeconds {
		log.Printf("recorder-hook readback failed: live segment duration mismatch")
		return false
	}
	expectedCabin := uint32(0)
	if settings.CabinRecording {
		expectedCabin = 1
	}
	if binary.LittleEndian.Uint32(applied[48:52]) != expectedCabin {
		log.Printf("recorder-hook readback failed: cabin recording mismatch")
		return false
	}
	return true
}

func restartDriveRecForRecorderHook(ctx context.Context) error {
	if !nativeFirmwareMatches(recorderHookInjector, recorderHookInjectorSHA) {
		return errors.New("recorder runtime injector unavailable")
	}
	if !nativeControlMu.TryLock() {
		return errors.New("another native control operation is in progress")
	}
	defer nativeControlMu.Unlock()
	release, err := lockNativeControlExisting()
	if err != nil {
		return err
	}
	defer release()
	text, err := command("systemctl", "show", "app-@DriveRec.service", "-p", "MainPID", "-p", "ActiveState", "-p", "SubState", "-p", "Job")
	if err != nil {
		return err
	}
	properties := map[string]string{}
	for _, line := range strings.Split(text, "\n") {
		if pair := strings.SplitN(line, "=", 2); len(pair) == 2 {
			properties[pair[0]] = pair[1]
		}
	}
	pid, err := strconv.ParseUint(properties["MainPID"], 10, 31)
	if err != nil || pid <= 1 || properties["ActiveState"] != "active" || properties["SubState"] != "running" || properties["Job"] != "" {
		return errors.New("DriveRec is not stable for runtime injection")
	}
	if !nativeFirmwareMatches("/proc/"+strconv.FormatUint(pid, 10)+"/exe", recorderDriveRecSHA) {
		return errors.New("DriveRec executable identity mismatch")
	}
	cmd := exec.CommandContext(ctx, recorderHookInjector, strconv.FormatUint(pid, 10), recorderHookLibrary)
	cmd.Env = []string{"PATH=/usr/bin:/bin"}
	output, runErr := cmd.CombinedOutput()
	if runErr != nil {
		return fmt.Errorf("recorder runtime injection failed: %w", runErr)
	}
	if string(output) != "injected=1\nreapply=scheduled\nregisters=restored\nstack=restored\n" {
		return errors.New("recorder runtime injection restoration was not confirmed")
	}
	deadline := time.Now().Add(25 * time.Second)
	for time.Now().Before(deadline) {
		if recorderAdvancedLoaded(ctx) {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
	return errors.New("recorder runtime apply read-back failed")
}

type recorderHealthFile struct {
	name     string
	size     int64
	modified time.Time
}

func recorderSDMounted() bool {
	data, err := os.ReadFile("/proc/mounts")
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 3 && fields[1] == SDRoot {
			return true
		}
	}
	return false
}

func recorderHealthSnapshot() (map[string]recorderHealthFile, error) {
	directory := filepath.Join(SDRoot, recordingDirectory)
	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil, err
	}
	if len(entries) > 20000 {
		return nil, errors.New("recording directory inventory is too large")
	}
	files := map[string]recorderHealthFile{}
	for _, entry := range entries {
		if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		camera, _, valid := parseRecordingFilename(entry.Name())
		if !valid {
			continue
		}
		info, statErr := entry.Info()
		if statErr != nil || !info.Mode().IsRegular() {
			continue
		}
		candidate := recorderHealthFile{entry.Name(), info.Size(), info.ModTime()}
		current, exists := files[camera]
		if !exists || candidate.modified.After(current.modified) || candidate.modified.Equal(current.modified) && candidate.name > current.name {
			files[camera] = candidate
		}
	}
	return files, nil
}

func recorderHealthAdvanced(before, after recorderHealthFile) bool {
	return after.name != "" && (before.name != after.name || after.size > before.size || after.modified.After(before.modified))
}

func verifyRecorderFunction(ctx context.Context) error {
	text, err := command("systemctl", "show", "app-@DriveRec.service", "-p", "MainPID", "-p", "ActiveState", "-p", "SubState", "-p", "Job")
	if err != nil {
		return err
	}
	properties := map[string]string{}
	for _, line := range strings.Split(text, "\n") {
		if pair := strings.SplitN(line, "=", 2); len(pair) == 2 {
			properties[pair[0]] = pair[1]
		}
	}
	pid, err := strconv.ParseUint(properties["MainPID"], 10, 31)
	if err != nil || pid <= 1 || properties["ActiveState"] != "active" || properties["SubState"] != "running" || properties["Job"] != "" {
		return errors.New("DriveRec is not running after settings apply")
	}
	tasks, err := os.ReadDir("/proc/" + strconv.FormatUint(pid, 10) + "/task")
	if err != nil || len(tasks) < 8 {
		return errors.New("DriveRec recording workers are unavailable")
	}
	encoderThreads := 0
	for _, task := range tasks {
		name, readErr := os.ReadFile("/proc/" + strconv.FormatUint(pid, 10) + "/task/" + task.Name() + "/comm")
		if readErr == nil && strings.HasPrefix(strings.TrimSpace(string(name)), "VideoEncodeStre") {
			encoderThreads++
		}
	}
	if encoderThreads < 2 {
		return errors.New("DriveRec encoder workers did not resume")
	}
	if !recorderSDMounted() {
		return nil
	}
	before, err := recorderHealthSnapshot()
	if err != nil {
		return fmt.Errorf("recording progress before apply: %w", err)
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(3 * time.Second):
	}
	after, err := recorderHealthSnapshot()
	if err != nil {
		return fmt.Errorf("recording progress after apply: %w", err)
	}
	settings, _, _, err := readRecorderAdvanced(recorderAdvancedConfigPath)
	if err != nil {
		return err
	}
	if !recorderHealthAdvanced(before["front"], after["front"]) {
		return errors.New("front recording did not resume on SD")
	}
	if settings.CabinRecording && !recorderHealthAdvanced(before["in"], after["in"]) {
		return errors.New("cabin recording did not resume on SD")
	}
	if !settings.CabinRecording && recorderHealthAdvanced(before["in"], after["in"]) {
		return errors.New("cabin recording did not stop on SD")
	}
	return nil
}

func recorderProcessMapsLibrary(pid uint32, library string) bool {
	data, err := os.ReadFile("/proc/" + strconv.FormatUint(uint64(pid), 10) + "/maps")
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 6 && fields[5] == library {
			return true
		}
	}
	return false
}

// EnsureRecorderHookAtBoot reconciles every new DriveRec process without a
// systemd drop-in. It injects before DriveRec creates its encoders, then proves
// exact process-memory settings and both recording outputs before committing
// the applied generation.
func EnsureRecorderHookAtBoot(ctx context.Context) error {
	if !recorderAdvancedMu.TryLock() {
		return errors.New("recorder settings operation in progress")
	}
	defer recorderAdvancedMu.Unlock()
	if !recorderAdvancedAvailable() {
		return nil
	}
	releaseApply, err := lockRecorderApply()
	if err != nil {
		return err
	}
	defer releaseApply()
	_, generation, configured, err := readRecorderAdvanced(recorderAdvancedConfigPath)
	if err != nil {
		return fmt.Errorf("recorder boot config read-back: %w", err)
	}
	// A save without "apply now" is deliberately pinned to the current
	// DriveRec process.  The periodic reconciler must leave that process alone;
	// as soon as its PID/start time changes, normal boot reconciliation applies
	// and verifies the saved generation.
	if configured && recorderDeferredForCurrent(generation) {
		return nil
	}
	_ = os.Remove(recorderAdvancedDeferredPath)
	loaded := recorderAdvancedLoaded(ctx)
	if configured && loaded && readRecorderApplied(recorderAdvancedAppliedPath) == generation {
		return nil
	}
	if !loaded {
		if err = recorderAdvancedRestart(ctx); err != nil {
			return fmt.Errorf("recorder boot hook restart: %w", err)
		}
	}
	if !recorderAdvancedLoaded(ctx) {
		return errors.New("recorder boot hook read-back failed")
	}
	if err = recorderAdvancedHealthy(ctx); err != nil {
		return fmt.Errorf("recorder boot health: %w", err)
	}
	if configured && readRecorderApplied(recorderAdvancedAppliedPath) != generation {
		if err = writeRecorderApplied(recorderAdvancedAppliedPath, generation); err != nil {
			return fmt.Errorf("recorder boot generation: %w", err)
		}
	}
	_ = os.Remove(recorderAdvancedDeferredPath)
	return nil
}

func recorderAdvancedState(ctx context.Context) (RecorderAdvancedState, error) {
	settings, generation, configured, err := readRecorderAdvanced(recorderAdvancedConfigPath)
	if err != nil {
		return RecorderAdvancedState{}, err
	}
	available := recorderAdvancedAvailable()
	loaded := available && recorderAdvancedLoaded(ctx)
	applied := configured && loaded && readRecorderApplied(recorderAdvancedAppliedPath) == generation
	return RecorderAdvancedState{Available: available, Configured: configured, HookLoaded: loaded, RestartRequired: configured && !applied, Generation: generation, Settings: settings, ObservedAt: time.Now().UTC(), Applied: applied}, nil
}

func HandleRecorderAdvanced(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method == http.MethodGet {
		state, err := recorderAdvancedState(r.Context())
		if err != nil {
			nativeError(w, 503, "RECORDER_HOOK_STATE_UNAVAILABLE", "録画エンコーダー設定を読み取れません")
			return
		}
		recordingJSON(w, 200, state)
		return
	}
	if r.Method != http.MethodPut {
		nativeError(w, 405, "METHOD_NOT_ALLOWED", "対応していない操作です")
		return
	}
	var body struct {
		ExpectedGeneration   uint32                   `json:"expectedGeneration"`
		Settings             RecorderAdvancedSettings `json:"settings"`
		ApplyNow             bool                     `json:"applyNow"`
		Confirmation         string                   `json:"confirmation"`
		AcknowledgeRecording bool                     `json:"acknowledgeRecordingGap"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&body) != nil || decoder.Decode(&struct{}{}) != io.EOF || !validRecorderAdvanced(body.Settings) {
		nativeError(w, 400, "INVALID_RECORDER_HOOK_SETTINGS", "ビットレート、レート制御、GOP間隔、分割時間を確認してください")
		return
	}
	if body.ApplyNow && (body.Confirmation != recorderApplyPhrase || !body.AcknowledgeRecording) {
		nativeError(w, 400, "RECORDER_RESTART_CONFIRMATION_REQUIRED", "録画が一時中断することを確認してください")
		return
	}
	if !recorderAdvancedAvailable() {
		nativeError(w, 503, "RECORDER_HOOK_UNAVAILABLE", "この本体では詳細録画設定を利用できません")
		return
	}
	if !recorderAdvancedMu.TryLock() {
		nativeError(w, 409, "RECORDER_HOOK_BUSY", "別の録画設定を適用中です。しばらくしてから再試行してください")
		return
	}
	defer recorderAdvancedMu.Unlock()
	_, generation, _, err := readRecorderAdvanced(recorderAdvancedConfigPath)
	if err != nil {
		nativeError(w, 503, "RECORDER_HOOK_STATE_UNAVAILABLE", "現在の録画設定を確認できません")
		return
	}
	if generation != body.ExpectedGeneration || generation == ^uint32(0) {
		nativeError(w, 409, "RECORDER_HOOK_STATE_CHANGED", "録画設定が変わりました。最新の状態を取得してください")
		return
	}
	var releaseApply func()
	if body.ApplyNow {
		releaseApply, err = lockRecorderApply()
		if err != nil {
			nativeError(w, 409, "RECORDER_HOOK_BUSY", "別の録画設定を適用中です。しばらくしてから再試行してください")
			return
		}
		defer releaseApply()
	}
	if err = os.Remove(recorderAdvancedAppliedPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		nativeError(w, 503, "RECORDER_HOOK_SAVE_FAILED", "録画設定の適用状態を安全に更新できません")
		return
	}
	if body.ApplyNow {
		if err = os.Remove(recorderAdvancedDeferredPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			nativeError(w, 503, "RECORDER_HOOK_SAVE_FAILED", "録画設定の適用方法を安全に更新できません")
			return
		}
	} else {
		identity, identityErr := recorderAdvancedIdentity()
		if identityErr != nil || writeRecorderDeferred(recorderAdvancedDeferredPath, generation+1, identity) != nil {
			nativeError(w, 503, "RECORDER_HOOK_SAVE_FAILED", "次回起動用の録画設定を安全に保存できません")
			return
		}
	}
	if err = writeRecorderAdvanced(recorderAdvancedConfigPath, body.Settings, generation+1); err != nil {
		nativeError(w, 503, "RECORDER_HOOK_SAVE_FAILED", "録画設定を安全に保存できません")
		return
	}
	state, _ := recorderAdvancedState(r.Context())
	state.Configured, state.Generation, state.Settings, state.RestartRequired, state.Applied = true, generation+1, body.Settings, true, false
	state.Message = "設定を保存しました。次回のドラレコ起動時に適用します。"
	if body.ApplyNow {
		if err = recorderAdvancedRestart(r.Context()); err != nil {
			state.Message = "設定を保存しましたが、即時適用できませんでした。もう一度お試しください。"
			recordingJSON(w, 202, state)
			return
		}
		if err = recorderAdvancedHealthy(r.Context()); err != nil {
			state.Message = "設定を保存しましたが、録画状態を確認できませんでした。"
			recordingJSON(w, 202, state)
			return
		}
		if err = writeRecorderApplied(recorderAdvancedAppliedPath, generation+1); err != nil {
			state.Message = "設定は反映されましたが、適用状態を保存できませんでした。"
			recordingJSON(w, 202, state)
			return
		}
		state.HookLoaded, state.RestartRequired, state.Applied = true, false, true
		state.Message = "設定を即時適用しました。"
		recordingJSON(w, 200, state)
		return
	}
	recordingJSON(w, 200, state)
}
