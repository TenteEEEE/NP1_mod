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
		fields, err := led.DecodeScreenObject(data, "colors")
		value, parseErr := strconv.ParseUint(string(fields["colors"]), 10, 8)
		colors := led.TrafficColors(value)
		if err != nil || parseErr != nil || !colors.Valid() {
			screenLEDJSON(w, http.StatusBadRequest, map[string]string{"reason": "信号のLED表示を確認してください"})
			return
		}
		if !ledApplyMu.TryLock() {
			w.WriteHeader(http.StatusConflict)
			return
		}
		defer ledApplyMu.Unlock()
		if colors != 0 && time.Since(preparedAt) >= 5*time.Second {
			if err := prepareScreenLED(); err != nil {
				screenLEDFailure(w, err)
				return
			}
			preparedAt = time.Now()
		}
		if err := led.SetTrafficColors(colors); err != nil {
			screenLEDFailure(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
}
