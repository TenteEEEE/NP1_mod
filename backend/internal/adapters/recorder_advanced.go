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
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	recorderHookSource     = "/overlay/owner-apps/np1-control/recorder-hook/libncc-driverec-hook.so"
	recorderHookLibrary    = "/run/np1-control-recorder-hook/libncc-driverec-hook.so"
	recorderHookConfig     = "/overlay/owner-apps/np1-control/recorder-hook/config"
	recorderHookApplied    = "/overlay/owner-apps/np1-control/recorder-hook/applied-generation"
	recorderHookLibrarySHA = "c6d4a270733366bbccbaf3191d4c7feed5ebd983041c4c96f67b44f547b764fa"
	recorderDriveRecSHA    = "000871dc36581f27ae831d9db3772b553a17cce0d814c52867276ac68ea0cde0"
	recorderConfigSize     = 64
	recorderConfigVersion  = 1
	recorderApplyPhrase    = "録画サービスを再起動"
	recorderMinimumBitrate = 128
	recorderMaximumBitrate = 50000
	recorderMinimumGOP     = 250
	recorderMaximumGOP     = 10000
	recorderMinimumSegment = 30
	recorderMaximumSegment = 600
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
var recorderAdvancedAvailable = func() bool { return nativeFirmwareMatches(recorderHookSource, recorderHookLibrarySHA) }
var recorderAdvancedLoaded = recorderHookLoaded
var recorderAdvancedRestart = restartDriveRecForRecorderHook
var recorderBootUptime = func() (time.Duration, error) {
	data, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return 0, err
	}
	fields := strings.Fields(string(data))
	if len(fields) == 0 {
		return 0, errors.New("missing uptime")
	}
	seconds, err := strconv.ParseFloat(fields[0], 64)
	if err != nil || seconds < 0 {
		return 0, errors.New("invalid uptime")
	}
	return time.Duration(seconds * float64(time.Second)), nil
}

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
	var imageBase, multimediaBase, hookStart, hookEnd uint64
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
	}
	if imageBase == 0 || multimediaBase == 0 || hookStart == 0 || hookEnd <= hookStart {
		log.Printf("recorder-hook readback failed: executable mapping unavailable")
		return false
	}
	memory, err := os.Open(proc + "/mem")
	if err != nil {
		log.Printf("recorder-hook readback failed: process memory unavailable")
		return false
	}
	defer memory.Close()
	settings, _, _, err := readRecorderAdvanced(recorderAdvancedConfigPath)
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
	return true
}

func restartDriveRecForRecorderHook(ctx context.Context) error {
	return errors.New("DriveRec standalone restart is disabled because it terminates stock recording")
}

// EnsureRecorderHookAtBoot reconciles the boot-order case where DriveRec is
// started before the persistent /etc overlay (and therefore its NCC-owned
// drop-in) becomes visible to systemd.  It is intentionally limited to the
// early boot window: a later broker restart must not interrupt an in-progress
// recording without an explicit owner action.
func EnsureRecorderHookAtBoot(ctx context.Context) error {
	uptime, err := recorderBootUptime()
	if err != nil {
		return fmt.Errorf("recorder boot uptime: %w", err)
	}
	if uptime > 15*time.Minute || !recorderAdvancedAvailable() || recorderAdvancedLoaded(ctx) {
		return nil
	}
	if err = recorderAdvancedRestart(ctx); err != nil {
		return fmt.Errorf("recorder boot hook restart: %w", err)
	}
	if !recorderAdvancedLoaded(ctx) {
		return errors.New("recorder boot hook read-back failed")
	}
	_, generation, configured, err := readRecorderAdvanced(recorderAdvancedConfigPath)
	if err != nil {
		return fmt.Errorf("recorder boot config read-back: %w", err)
	}
	if configured && readRecorderApplied(recorderAdvancedAppliedPath) != generation {
		if err = writeRecorderApplied(recorderAdvancedAppliedPath, generation); err != nil {
			return fmt.Errorf("recorder boot generation: %w", err)
		}
	}
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
		nativeError(w, 400, "INVALID_RECORDER_HOOK_SETTINGS", "ビットレート、レート制御、GOP間隔を確認してください")
		return
	}
	if body.ApplyNow && (body.Confirmation != recorderApplyPhrase || !body.AcknowledgeRecording) {
		nativeError(w, 400, "RECORDER_RESTART_CONFIRMATION_REQUIRED", "録画が一時中断することを確認してください")
		return
	}
	if !recorderAdvancedAvailable() {
		nativeError(w, 503, "RECORDER_HOOK_UNAVAILABLE", "対応する録画フックが配備されていません")
		return
	}
	if !recorderAdvancedMu.TryLock() {
		nativeError(w, 409, "RECORDER_HOOK_BUSY", "別の録画設定操作を確認中です")
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
	if err = os.Remove(recorderAdvancedAppliedPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		nativeError(w, 503, "RECORDER_HOOK_SAVE_FAILED", "録画設定の適用状態を安全に更新できません")
		return
	}
	if err = writeRecorderAdvanced(recorderAdvancedConfigPath, body.Settings, generation+1); err != nil {
		nativeError(w, 503, "RECORDER_HOOK_SAVE_FAILED", "録画設定を安全に保存できません")
		return
	}
	state, _ := recorderAdvancedState(r.Context())
	state.Configured, state.Generation, state.Settings, state.RestartRequired, state.Applied = true, generation+1, body.Settings, true, false
	state.Message = "設定を保存しました。DriveRecの次回起動時に適用されます。"
	if body.ApplyNow {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 25*time.Second)
		defer cancel()
		if err = recorderAdvancedRestart(ctx); err != nil {
			state.Message = "設定は保存しましたが、録画サービスの再起動を確認できません。次回起動時に適用されます。"
			recordingJSON(w, 202, state)
			return
		}
		state.HookLoaded = recorderAdvancedLoaded(ctx)
		state.RestartRequired = !state.HookLoaded
		if state.HookLoaded {
			if err = writeRecorderApplied(recorderAdvancedAppliedPath, generation+1); err != nil {
				state.RestartRequired = true
				state.Message = "録画サービスと起動時フックは確認しましたが、適用済み世代を記録できません。再適用してください。"
				recordingJSON(w, 202, state)
				return
			}
			state.Applied = true
			state.Message = "録画サービスを再起動し、起動時フックと設定世代の読込みを確認しました。"
		} else {
			state.Message = "録画サービスは再起動しましたが、起動時フックを確認できません。純正動作を維持しています。"
		}
	}
	recordingJSON(w, 200, state)
}
