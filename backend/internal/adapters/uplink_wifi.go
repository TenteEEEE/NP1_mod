package adapters

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"time"

	"github.com/np1-control-center/ncc/internal/config"
)

const uplinkWiFiUseFile = uplinkPrivate + "/wifi-use.json"

// Both NCC processes hold the same lock while deciding or changing STA. The
// lease covers the time between requests and survives a broker restart.
type uplinkWiFiUse struct {
	Until      time.Time `json:"until"`
	RestoreOff bool      `json:"restoreOff"`
	Confirmed  bool      `json:"confirmed"`
}

func readUplinkWiFiUse() (uplinkWiFiUse, error) {
	var use uplinkWiFiUse
	raw, err := os.ReadFile(uplinkWiFiUseFile)
	if os.IsNotExist(err) {
		return use, nil
	}
	if err == nil {
		err = json.Unmarshal(raw, &use)
	}
	return use, err
}

func startUplinkWiFiUse(ctx context.Context) (uplinkWiFiUse, error) {
	mode := uplinkMode()
	if mode == UplinkModeCellular {
		return uplinkWiFiUse{}, errors.New("WIFI_CELLULAR_FIXED")
	}
	on, err := stockSTAOn()
	if err != nil {
		return uplinkWiFiUse{}, errors.New("WIFI_STATION_UNAVAILABLE")
	}
	previous, err := readUplinkWiFiUse()
	if err != nil {
		return uplinkWiFiUse{}, errors.New("WIFI_SETUP_UNAVAILABLE")
	}
	state := readUplinkState()
	use := uplinkWiFiUse{
		Until:      time.Now().Add(90 * time.Second),
		RestoreOff: mode == UplinkModeAuto && (!on || !previous.Confirmed && previous.RestoreOff || state.ProbeUntil != nil && !wifiDefaultRouteWorking()),
	}
	if err := config.Atomic(uplinkWiFiUseFile, use); err != nil {
		return uplinkWiFiUse{}, errors.New("WIFI_SETUP_UNAVAILABLE")
	}
	if !on {
		if err := requestSTA(ctx, true); err != nil {
			return use, errors.New("WIFI_STATION_UNAVAILABLE")
		}
	} else if _, err := os.Stat(stockWPASocket); os.IsNotExist(err) && !wifiCarries() {
		_ = reconcileSTAOn(ctx)
	}
	ready, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	for ready.Err() == nil {
		if raw, err := vendorWPAContext(ready, "PING"); err == nil && strings.TrimSpace(raw) == "PONG" {
			return use, nil
		}
		select {
		case <-ready.Done():
		case <-time.After(300 * time.Millisecond):
		}
	}
	return use, errors.New("WIFI_CONTROL_UNAVAILABLE")
}

func withUplinkWiFi[T any](request context.Context, run func(context.Context, *uplinkWiFiUse) (T, error)) (result T, err error) {
	if !wifiController.usesStockStation() {
		return run(request, &uplinkWiFiUse{})
	}
	// Changing the uplink can temporarily cut the initiating HTTP connection.
	// Complete the bounded operation and leave the journal for the next visit.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(request), 85*time.Second)
	defer cancel()
	wait, stop := context.WithTimeout(ctx, 12*time.Second)
	unlock, err := lockUplinkWiFi(wait)
	stop()
	if err != nil {
		return result, errors.New("WIFI_SETUP_PENDING")
	}
	defer unlock()
	if err := wifiController.Recover(ctx); err != nil {
		return result, err
	}
	if wifiController.hasPending() {
		return result, errors.New("WIFI_TRANSACTION_PENDING")
	}
	use, err := startUplinkWiFiUse(ctx)
	defer func() {
		if !use.Until.IsZero() {
			// A scan (or failed connection setup) releases STA immediately.
			if saveErr := config.Atomic(uplinkWiFiUseFile, use); saveErr != nil && err == nil {
				err = errors.New("WIFI_SETUP_UNAVAILABLE")
			}
		}
	}()
	if err != nil {
		if !use.Until.IsZero() {
			use.Until = time.Now()
		}
		return result, err
	}
	use.Until = time.Now()
	return run(ctx, &use)
}

func connectWithUplink(ctx context.Context, connect func(context.Context) (WiFiTransaction, error)) (WiFiTransaction, error) {
	return withUplinkWiFi(ctx, func(ctx context.Context, use *uplinkWiFiUse) (WiFiTransaction, error) {
		if wifiController.usesStockStation() {
			// Reserve enough time for the journal and its independent rollback
			// even if the broker restarts just after creating the transaction.
			reserved := *use
			reserved.Until = time.Now().Add(2 * time.Minute)
			if err := config.Atomic(uplinkWiFiUseFile, reserved); err != nil {
				return WiFiTransaction{}, errors.New("WIFI_SETUP_UNAVAILABLE")
			}
		}
		transaction, err := connect(ctx)
		if err == nil {
			// Allow the independent rollback timer to finish before STA is stopped.
			use.Until = transaction.ExpiresAt.Add(15 * time.Second)
		} else if wifiController.hasPending() {
			// A failed or interrupted attempt can still own a rollback journal.
			use.Until = time.Now().Add(30 * time.Second)
		}
		return transaction, err
	})
}

func finishWiFiUse(ctx context.Context, id string, confirm bool) (WiFiTransaction, error) {
	finish := wifiController.Rollback
	if confirm {
		finish = wifiController.Confirm
	}
	if !wifiController.usesStockStation() {
		return finish(ctx, id)
	}
	wait, cancel := context.WithTimeout(context.WithoutCancel(ctx), 18*time.Second)
	defer cancel()
	unlock, err := lockUplinkWiFi(wait)
	if err != nil {
		return WiFiTransaction{}, errors.New("WIFI_SETUP_UNAVAILABLE")
	}
	defer unlock()
	use, err := readUplinkWiFiUse()
	if err != nil {
		return WiFiTransaction{}, errors.New("WIFI_SETUP_UNAVAILABLE")
	}
	transaction, err := finish(wait, id)
	if err != nil {
		return transaction, err
	}
	use.Until = time.Now()
	use.Confirmed = transaction.State == "confirmed"
	if err := config.Atomic(uplinkWiFiUseFile, use); err != nil {
		return transaction, errors.New("WIFI_SETUP_UNAVAILABLE")
	}
	return transaction, nil
}
