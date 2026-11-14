package api

import (
	"net/http"
	"strconv"
	"time"

	"github.com/np1-control-center/ncc/internal/radio"
	"github.com/np1-control-center/ncc/internal/trips"
)

func (s *Server) radioHistory(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	days := 7
	if q.Has("days") {
		var err error
		days, err = strconv.Atoi(q.Get("days"))
		if err != nil || days < 1 || days > 365 {
			fail(w, 400, "INVALID_PERIOD", "期間を確認してください")
			return
		}
	}
	size := 50
	if q.Has("cellSize") {
		var err error
		size, err = strconv.Atoi(q.Get("cellSize"))
		if err != nil {
			fail(w, 400, "INVALID_GRID", "グリッド幅を確認してください")
			return
		}
	}
	metric := q.Get("metric")
	if metric == "" {
		metric = "rsrp"
	}
	now := time.Now()
	from, to := now.AddDate(0, 0, -days), now
	if q.Has("date") {
		day, err := trips.TrackDate(q.Get("date"))
		if err != nil || len(q["date"]) != 1 || q.Has("days") {
			fail(w, 400, "INVALID_PERIOD", "日付を確認してください")
			return
		}
		from, to = day, day.AddDate(0, 0, 1).Add(-time.Nanosecond)
		if now.Before(to) {
			to = now
		}
	}
	history := s.radioStore
	if history == nil {
		history = radio.NewHistory(s.cfg.StateDir)
	}
	result, err := history.Query(radio.Query{From: from, To: to, Metric: metric, Carrier: q.Get("carrier"), Band: q.Get("band"), CellSize: size, TripWeighted: q.Get("tripWeighted") == "true", IncludeUnknownAccuracy: q.Get("includeUnknownAccuracy") == "true"})
	if err != nil {
		fail(w, 400, "RADIO_QUERY_FAILED", "電波履歴を取得できません")
		return
	}
	jsonOut(w, 200, result)
}
