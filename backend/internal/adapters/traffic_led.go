package adapters

import (
	"net/http"
	"strconv"
	"time"

	"github.com/np1-control-center/ncc/internal/led"
)

func RegisterTrafficLEDHandler(mux *http.ServeMux) {
	var preparedAt time.Time
	mux.HandleFunc("PUT /led/traffic", func(w http.ResponseWriter, r *http.Request) {
		data, ok := screenLEDBody(w, r)
		if !ok {
			return
		}
		fields, err := led.DecodeScreenObject(data, "colors", "expiresAtMs", "visitUntilMs")
		value, parseErr := strconv.ParseUint(string(fields["colors"]), 10, 8)
		unix, expiryErr := strconv.ParseInt(string(fields["expiresAtMs"]), 10, 64)
		deadline := time.UnixMilli(unix)
		visitMS, visitErr := strconv.ParseInt(string(fields["visitUntilMs"]), 10, 64)
		visitUntil := time.UnixMilli(visitMS)
		colors := led.TrafficColors(value)
		now := time.Now()
		if err != nil || parseErr != nil || expiryErr != nil || visitErr != nil || !colors.Valid() ||
			deadline.After(now.Add(2*time.Second)) || visitUntil.After(now.Add(10*time.Second)) {
			screenLEDJSON(w, http.StatusBadRequest, map[string]string{"reason": "信号のLED表示を確認してください"})
			return
		}
		if !ledApplyMu.TryLock() {
			w.WriteHeader(http.StatusConflict)
			return
		}
		defer ledApplyMu.Unlock()
		// Publish before starting the worker so its first frame can already use
		// the PICS overlay instead of the configured background animation.
		if err := led.SetTrafficColors(colors, deadline, visitUntil); err != nil {
			screenLEDFailure(w, err)
			return
		}
		if now.Before(visitUntil) && time.Since(preparedAt) >= 5*time.Second {
			if err := prepareScreenLED(); err != nil {
				screenLEDFailure(w, err)
				return
			}
			preparedAt = time.Now()
		}
		if r.Context().Err() != nil {
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
}
