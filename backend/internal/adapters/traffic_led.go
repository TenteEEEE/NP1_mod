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
		fields, err := led.DecodeScreenObject(data, "colors", "expiresAtMs")
		value, parseErr := strconv.ParseUint(string(fields["colors"]), 10, 8)
		unix, expiryErr := strconv.ParseInt(string(fields["expiresAtMs"]), 10, 64)
		deadline := time.UnixMilli(unix)
		colors := led.TrafficColors(value)
		if err != nil || parseErr != nil || expiryErr != nil || !colors.Valid() || deadline.After(time.Now().Add(2*time.Second)) {
			screenLEDJSON(w, http.StatusBadRequest, map[string]string{"reason": "信号のLED表示を確認してください"})
			return
		}
		if !ledApplyMu.TryLock() {
			w.WriteHeader(http.StatusConflict)
			return
		}
		defer ledApplyMu.Unlock()
		if colors != 0 && time.Now().Before(deadline) && time.Since(preparedAt) >= 5*time.Second {
			if err := prepareScreenLED(); err != nil {
				screenLEDFailure(w, err)
				return
			}
			preparedAt = time.Now()
		}
		if r.Context().Err() != nil {
			return
		}
		if err := led.SetTrafficColors(colors, deadline); err != nil {
			screenLEDFailure(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
}
