package adapters

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/np1-control-center/ncc/internal/config"
)

// Paths of the uplink supervisor (unit np1-control-uplink).
const (
	UplinkRuntime   = "/run/np1-control-uplink"
	uplinkHelper    = UplinkRuntime + "/helper"
	UplinkStatus    = UplinkRuntime + "/status.json"
	uplinkPrivate   = "/overlay/owner-apps/np1-control/private/uplink"
	uplinkStateFile = uplinkPrivate + "/state.json"
	// UplinkSettings is written by the broker for the owner's on/off choice.
	UplinkSettings  = uplinkPrivate + "/settings.json"
	stockSettingMap = "/tmp/victoria/victoria_PMEM_AL_SRV_SET_SETTING_ITEM.map"
)

type UplinkStatusView struct {
	At           time.Time   `json:"at"`
	Mode         string      `json:"mode"`
	STAOn        *bool       `json:"staOn"`
	WPAState     string      `json:"wpaState,omitempty"`
	WiFiInternet string      `json:"wifiInternet,omitempty"`
	Uplink       string      `json:"uplink"` // wifi | cellular | none
	State        UplinkState `json:"state"`
	ScanKnown    bool        `json:"scanKnown"`
	SavedSeen    bool        `json:"savedSeen"`
	Error        string      `json:"error,omitempty"`
	HelperSeen   bool        `json:"helperAvailable"`
}

// stockSTAOn reads stock setting 40 (0 = STA mode on, 1 = off) from the shared settings
// map, with the same layout checks as the field trial.
func stockSTAOn() (bool, error) {
	raw, e := os.ReadFile(stockSettingMap)
	if e != nil {
		return false, e
	}
	at := func(offset int) uint32 { return binary.LittleEndian.Uint32(raw[offset:]) }
	if len(raw) != 7296 || at(0) != 7168 || at(4) != 0 || at(128) != 1 || at(132) != 1 || at(140) != 0 || at(148) != 44 || at(152) != 86 {
		return false, errors.New("UPLINK_STOCK_LAYOUT")
	}
	switch at(128 + 44 + 4*40) {
	case 0:
		return true, nil
	case 1:
		return false, nil
	}
	return false, errors.New("UPLINK_STOCK_VALUE")
}

func communicationPID() (string, error) {
	entries, e := os.ReadDir("/proc")
	if e != nil {
		return "", e
	}
	found := ""
	for _, entry := range entries {
		if _, e := strconv.Atoi(entry.Name()); e != nil {
			continue
		}
		if exe, e := os.Readlink(filepath.Join("/proc", entry.Name(), "exe")); e == nil && exe == "/usr/local/bin/Communication" {
			if found != "" {
				return "", errors.New("UPLINK_OWNER_AMBIGUOUS")
			}
			found = entry.Name()
		}
	}
	if found == "" {
		return "", errors.New("UPLINK_OWNER_MISSING")
	}
	return found, nil
}

var helperWire = regexp.MustCompile(`(?m)^wifi-sta-wire=([12])$`)
var staOwnerOffMismatch = regexp.MustCompile(`(?m)^STA read-back stage=STA-get owner=2 native40=0$`)

func runSTAHelper(ctx context.Context, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, uplinkHelper, args...)
	cmd.Env = []string{"PATH=/usr/bin:/bin", "LD_LIBRARY_PATH=/usr/local/lib", "NCC_NATIVE_WIFI_STA_TRIAL=1"}
	return cmd.CombinedOutput()
}

func applySTA(ctx context.Context, pid, expected, next string, carries func() bool, invoke func(context.Context, ...string) ([]byte, error)) ([]byte, error) {
	out, err := invoke(ctx, "--apply", pid, expected, next)
	if next == "2" && err != nil && ctx.Err() == nil && staOwnerOffMismatch.Match(out) && !carries() {
		return invoke(ctx, "--reconcile", pid, next)
	}
	return out, err
}

func reconcileSTAOn(ctx context.Context) error {
	if wifiCarries() {
		return errors.New("UPLINK_STA_LINK_PRESENT")
	}
	pid, err := communicationPID()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	out, err := runSTAHelper(ctx, "--reconcile", pid, "1")
	if wire := helperWire.FindSubmatch(out); err != nil || wire == nil || string(wire[1]) != "1" {
		return errors.New("UPLINK_REARM_UNCONFIRMED")
	}
	return nil
}

// requestSTA sends the verified owner request (1 = ON, 2 = OFF) and then confirms the
// result from setting 40, which is the state Communication itself acts on. The helper's
// exit status is only advisory: its strict shared-memory reference check can fail when
// another stock process maps the same region at that moment, even though the request
// completed (seen in the 2026-11-13 trial).
func requestSTA(ctx context.Context, on bool) error {
	pid, e := communicationPID()
	if e != nil {
		return e
	}
	expected, next := "2", "1"
	if !on {
		expected, next = "1", "2"
	}
	ctx, cancel := context.WithTimeout(ctx, 40*time.Second)
	defer cancel()
	out, runErr := applySTA(ctx, pid, expected, next, wifiCarries, runSTAHelper)
	if m := helperWire.FindSubmatch(out); runErr != nil && m == nil {
		log.Printf("uplink helper: %v", runErr)
	}
	deadline := time.Now().Add(15 * time.Second)
	for ctx.Err() == nil && time.Now().Before(deadline) {
		if state, e := stockSTAOn(); e == nil && state == on {
			return nil
		}
		select {
		case <-ctx.Done():
		case <-time.After(500 * time.Millisecond):
		}
	}
	return errors.New("UPLINK_REQUEST_UNCONFIRMED")
}

// scanForSaved asks the supplicant (if it still runs with the mode off) for a scan and
// reports whether a saved SSID is heard at a usable level.
func scanForSaved(ctx context.Context, saved []string) (known, seen bool) {
	if len(saved) == 0 {
		return false, false
	}
	ctx, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()
	result, e := wifiController.Scan(ctx)
	if e != nil {
		return false, false
	}
	want := map[string]bool{}
	for _, s := range saved {
		want[s] = true
	}
	for _, found := range result.Results {
		if want[found.SSID] && found.SignalDBm >= -80 {
			return true, true
		}
	}
	return true, false
}

// Uplink modes chosen by the owner on the Wi-Fi/AP page (written by the broker).
const (
	UplinkModeAuto     = "auto"     // switch to 4G when out of Wi-Fi range, back near saved Wi-Fi
	UplinkModeWiFi     = "wifi"     // keep the stock STA mode on
	UplinkModeCellular = "cellular" // keep the stock STA mode off (4G)
)

type UplinkOwnerSettings struct {
	Mode string `json:"mode"`
}

func ValidUplinkMode(mode string) bool {
	return mode == UplinkModeAuto || mode == UplinkModeWiFi || mode == UplinkModeCellular
}
func uplinkMode() string {
	var v UplinkOwnerSettings
	if raw, e := os.ReadFile(UplinkSettings); e == nil && json.Unmarshal(raw, &v) == nil && ValidUplinkMode(v.Mode) {
		return v.Mode
	}
	return UplinkModeAuto
}

// stationLinkUp reads the station's real link state. A default route can stay in
// the table after the access point disappears (it is then flagged "linkdown"),
// so the route alone does not prove that Wi-Fi carries traffic.
func stationLinkUp(operstate, carrier string, carrierKnown bool) bool {
	if operstate != "up" {
		return false
	}
	return !carrierKnown || carrier == "1"
}

func wifiCarries() bool {
	operstate, e := os.ReadFile("/sys/class/net/wlan0/operstate")
	if e != nil {
		return false
	}
	carrier, carrierErr := os.ReadFile("/sys/class/net/wlan0/carrier")
	return stationLinkUp(strings.TrimSpace(string(operstate)), strings.TrimSpace(string(carrier)), carrierErr == nil)
}

func wifiDefaultRouteWorking() bool {
	return currentUplink() == "wifi" && wifiCarries()
}

func currentUplink() string {
	iface := defaultIface()
	switch iface {
	case "wlan0":
		return "wifi"
	case "":
		return "none"
	}
	if strings.HasPrefix(iface, "usb0") || strings.HasPrefix(iface, "rmnet") {
		return "cellular"
	}
	return "other"
}

func readUplinkState() UplinkState {
	var state UplinkState
	if raw, e := os.ReadFile(uplinkStateFile); e == nil {
		_ = json.Unmarshal(raw, &state)
	}
	return state
}

// RunUplinkSupervisor ticks every 10 seconds until ctx ends.
func RunUplinkSupervisor(ctx context.Context) error {
	if e := os.MkdirAll(uplinkPrivate, 0700); e != nil {
		return e
	}
	state := readUplinkState()
	var saved []string
	savedKnown := false
	if raw, e := os.ReadFile(filepath.Join(uplinkPrivate, "saved.json")); e == nil {
		savedKnown = json.Unmarshal(raw, &saved) == nil && saved != nil
	}
	previousMode := ""
	var manualRetry time.Time
	var nextProfileRefresh time.Time
	var health uplinkWiFiHealth
	var lease wifiDHCPManager
	defer func() { lease.release(context.Background()) }()
	for {
		mode := uplinkMode()
		status := UplinkStatusView{At: time.Now().UTC(), Mode: mode}
		_, helperErr := os.Stat(uplinkHelper)
		status.HelperSeen = helperErr == nil
		wait, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
		unlock, lockErr := lockUplinkWiFi(wait)
		cancel()
		if lockErr == nil {
			// Read the mode again inside the lock, alongside manual setup and PUT.
			mode = uplinkMode()
			status.Mode = mode
			on, stockErr := stockSTAOn()
			use, useErr := readUplinkWiFiUse()
			if stockErr == nil && useErr == nil {
				status.STAOn = &on
				now := time.Now()
				if mode != previousMode {
					manualRetry = time.Time{}
					// Entering automatic mode with the STA mode off (for example after manual 4G)
					// lets the automation bring Wi-Fi back near a saved network.
					if mode == UplinkModeAuto && previousMode != "" && !on && state.AutoOffAt == nil {
						t := now
						state.AutoOffAt = &t
						// The owner just asked for automatic: try the station at once
						// instead of waiting for the blind-retry interval.
						state.NextProbeAt = &t
					}
					previousMode = mode
				}
				if on {
					status.WPAState = wpaState()
				} else {
					nextProfileRefresh = time.Time{}
					health = uplinkWiFiHealth{}
				}
				if use.Confirmed {
					state.AutoOffAt, state.RetryAfter, state.NextProbeAt, state.ProbeUntil, state.UnconnectedSince = nil, nil, nil, nil, nil
					nextProfileRefresh = time.Time{}
				}
				setupActive := now.Before(use.Until)
				restoreOff := !setupActive && use.RestoreOff && !use.Confirmed && mode == UplinkModeAuto
				if on && !setupActive && !restoreOff && (nextProfileRefresh.IsZero() || !now.Before(nextProfileRefresh)) {
					restore, stop := context.WithTimeout(ctx, 6*time.Second)
					list, err := wifiController.restoreProfiles(restore)
					stop()
					if err == nil {
						saved, savedKnown = list, true
						_ = config.Atomic(filepath.Join(uplinkPrivate, "saved.json"), saved)
						nextProfileRefresh = now.Add(45 * time.Second)
					}
				}
				lease.tick(ctx, on && !restoreOff, wifiController)
				action, reason, next := UplinkNone, "", state
				switch {
				case setupActive:
					// The broker owns STA during scan/association and rollback.
				case restoreOff:
					if on && status.HelperSeen && now.After(manualRetry) {
						action, reason = UplinkOff, "wifi-setup-ended"
					}
					if state.AutoOffAt != nil || state.ProbeUntil != nil {
						probe := now.Add(uplinkProbeEvery)
						next.AutoOffAt, next.NextProbeAt = &now, &probe
					}
					next.ProbeUntil = nil
				case mode == UplinkModeWiFi || mode == UplinkModeCellular:
					// Manual: hold the chosen mode; automatic markers do not apply.
					want := mode == UplinkModeWiFi
					next.AutoOffAt, next.UnconnectedSince, next.RetryAfter = nil, nil, nil
					next.NextProbeAt, next.ProbeUntil = nil, nil
					if want != on && status.HelperSeen && now.After(manualRetry) {
						action, reason = UplinkOff, "owner-manual"
						if want {
							action = UplinkOn
						}
					}
				default:
					link := on && wifiDefaultRouteWorking()
					working := health.working(now, link, func() bool { return probeWiFiInternet(ctx) })
					if link {
						status.WiFiInternet = "unverified"
						if working && health.misses == 0 {
							status.WiFiInternet = "verified"
						}
					}
					in := UplinkInputs{Now: now, STAOn: on, Working: working, Enabled: status.HelperSeen}
					if fix, e := liveGNSS.LatestWithin(ctx, now, uplinkFixMaxAge); e == nil && fix != nil {
						in.Position = &UplinkPosition{At: fix.At, Lat: fix.Lat, Lon: fix.Lon}
					}
					if !on && state.AutoOffAt != nil && (state.RetryAfter == nil || !now.Before(*state.RetryAfter)) {
						if savedKnown && len(saved) == 0 {
							profiles, e := wifiController.loadProfiles()
							in.NoTargets = e == nil && len(profiles) == 0
						}
						in.ScanKnown, in.SavedSeen = scanForSaved(ctx, saved)
						status.ScanKnown, status.SavedSeen = in.ScanKnown, in.SavedSeen
					}
					if status.HelperSeen && now.After(manualRetry) {
						action, reason, next = DecideUplink(in, state)
					}
				}
				if action != UplinkNone {
					if e := requestSTA(ctx, action == UplinkOn); e != nil {
						status.Error = e.Error()
						// Keep the previous state so the decision is retried later.
						next = state
						manualRetry = time.Now().Add(15 * time.Second)
					} else {
						on = action == UplinkOn
						if !on {
							lease.release(ctx)
							status.WiFiInternet = ""
						}
						if on && next.ProbeUntil != nil {
							// The probe begins after the stock request has applied.
							until := time.Now().Add(uplinkProbeWindow)
							next.ProbeUntil = &until
						}
						next.Switches++
						next.LastAction, next.LastReason = string(action), reason
						log.Printf("uplink: STA %s (%s)", action, reason)
					}
				}
				state = next
				if err := config.Atomic(uplinkStateFile, state); err == nil && !setupActive && !use.Until.IsZero() && (!restoreOff || !on) {
					_ = os.Remove(uplinkWiFiUseFile)
				}
			} else if stockErr != nil {
				status.Error = "UPLINK_STOCK_STATE_UNAVAILABLE"
			} else {
				status.Error = "UPLINK_WIFI_SETUP_UNAVAILABLE"
			}
			unlock()
		} else {
			// A request is starting Wi-Fi or waiting for a scan. Continue showing
			// the actual uplink without blocking the status API.
			if on, err := stockSTAOn(); err == nil {
				status.STAOn = &on
			}
		}
		status.At = time.Now().UTC()
		status.State = state
		status.Uplink = currentUplink()
		_ = config.Atomic(UplinkStatus, status)
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(10 * time.Second):
		}
	}
}

// UplinkView is what the Wi-Fi/AP page shows: the owner's mode and the supervisor's latest
// round (null when older than a minute, never a remembered value).
func UplinkView() map[string]any {
	view := map[string]any{"mode": uplinkMode(), "status": nil}
	if raw, e := os.ReadFile(UplinkStatus); e == nil {
		var s UplinkStatusView
		if json.Unmarshal(raw, &s) == nil && time.Since(s.At) < time.Minute {
			view["status"] = s
		}
	}
	return view
}

// HandleUplink serves GET/PUT /network/uplink on the root broker.
func HandleUplink(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		recordingJSON(w, 200, UplinkView())
		return
	}
	var body UplinkOwnerSettings
	if !wifiDecode(w, r, &body) {
		return
	}
	if !ValidUplinkMode(body.Mode) {
		nativeError(w, 400, "INVALID_UPLINK_MODE", "回線の切替方法を選んでください")
		return
	}
	wait, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	unlock, err := lockUplinkWiFi(wait)
	if err != nil {
		wifiHTTPError(w, errors.New("WIFI_SETUP_PENDING"))
		return
	}
	defer unlock()
	if wifiController.hasPending() {
		wifiHTTPError(w, errors.New("WIFI_TRANSACTION_PENDING"))
		return
	}
	if e := config.Atomic(UplinkSettings, body); e != nil {
		nativeError(w, 503, "UPLINK_SETTINGS_UNAVAILABLE", "設定を保存できません")
		return
	}
	recordingJSON(w, 200, UplinkView())
}
