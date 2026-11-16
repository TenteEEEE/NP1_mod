package adapters

import (
	"github.com/np1-control-center/ncc/internal/model"
	"github.com/np1-control-center/ncc/internal/trips"
	"net/http"
	"time"
)

func HandleRecordingTrack(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		recordingJSON(w, 405, map[string]string{"reason": "GETのみ使用できます"})
		return
	}
	query := r.URL.Query()
	date := query.Get("date")
	var from, to time.Time
	valid := false
	if len(query) == 1 && len(query["date"]) == 1 {
		_, err := trips.TrackDate(date)
		valid = err == nil
	} else if len(query) == 2 && len(query["from"]) == 1 && len(query["to"]) == 1 {
		var fromErr, toErr error
		from, fromErr = time.Parse(time.RFC3339Nano, query.Get("from"))
		to, toErr = time.Parse(time.RFC3339Nano, query.Get("to"))
		valid = fromErr == nil && toErr == nil && !from.IsZero() && from.Before(to) && to.Sub(from) <= 24*time.Hour
	}
	if !valid {
		recordingJSON(w, 400, map[string]string{"reason": "日付、または24時間以内の開始・終了時刻を指定してください"})
		return
	}
	snapshot, err := tripIndex.Read(r.Context())
	if err != nil {
		recordingJSON(w, 503, map[string]string{"reason": "位置記録を取得できません"})
		return
	}
	var track trips.TrackReport
	if date != "" {
		track, err = trips.DayTrack(snapshot, date, time.Now())
	} else {
		track, err = trips.WindowTrack(snapshot, from, to, time.Now())
	}
	if err != nil {
		recordingJSON(w, 400, map[string]string{"reason": "日付が不正です"})
		return
	}
	recordingJSON(w, 200, model.Value(track.Source, track))
}
