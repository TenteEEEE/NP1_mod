package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/np1-control-center/ncc/internal/model"
	"github.com/np1-control-center/ncc/internal/radio"
	"github.com/np1-control-center/ncc/internal/trips"
)

func (s *Server) collectTracks(ctx context.Context) {
	timer := time.NewTicker(5 * time.Minute)
	defer timer.Stop()
	for {
		now := time.Now()
		retentionError := ""
		for offset := 6; offset >= 0; offset-- {
			at := now.AddDate(0, 0, -offset)
			day := at.In(trips.Japan).Format("2006-01-02")
			request, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://broker/recordings/track?date="+day, nil)
			response, err := s.client.Do(request)
			if err == nil {
				var value struct {
					Data trips.TrackReport `json:"data"`
				}
				if response.StatusCode == 200 && json.NewDecoder(io.LimitReader(response.Body, 32*1024*1024+1)).Decode(&value) == nil && value.Data.Points != nil {
					if _, e := s.tracks.Ingest(value.Data, now); e != nil {
						retentionError = "track-retention-unavailable"
					}
				}
				response.Body.Close()
			}
			if err != nil {
				retentionError = "track-source-unavailable"
			}
			if s.pics != nil {
				track, e := s.tracks.Read(day)
				if e == nil {
					e = s.pics.backfillPICSHistory(day, track)
				}
				if e != nil {
					slog.Warn("PICS history position backfill failed", "day", day)
				}
			}
		}
		s.loggerMu.Lock()
		s.logger.TrackRetentionError = retentionError
		s.logger.LastTrackRetentionAt = now
		s.loggerMu.Unlock()
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
	}
}

func (s *Server) recordedTrack(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	now := time.Now().UTC()
	var from, to time.Time
	date := q.Get("date")
	var err error
	if len(q) == 1 && len(q["date"]) == 1 {
		from, err = trips.TrackDate(date)
		to = from.AddDate(0, 0, 1).Add(-time.Nanosecond)
	} else if len(q) == 2 && len(q["from"]) == 1 && len(q["to"]) == 1 {
		from, err = time.Parse(time.RFC3339Nano, q.Get("from"))
		if err == nil {
			to, err = time.Parse(time.RFC3339Nano, q.Get("to"))
		}
		if !from.Before(to) || to.Sub(from) > 24*time.Hour {
			err = errors.New("invalid track window")
		}
	} else {
		err = errors.New("invalid track query")
	}
	if err != nil {
		fail(w, 400, "INVALID_TRACK_RANGE", "地図の日付・時刻範囲を確認してください")
		return
	}
	var samples []trips.Sample
	partial := false
	retained := 0
	for day := from.In(trips.Japan); day.Format("2006-01-02") <= to.In(trips.Japan).Format("2006-01-02"); day = day.AddDate(0, 0, 1) {
		report, e := s.tracks.Read(day.Format("2006-01-02"))
		if e != nil {
			fail(w, 503, "TRACK_STORAGE_UNAVAILABLE", "保存したGPS記録を読み取れません")
			return
		}
		samples = append(samples, trips.TrackSamples(report)...)
		retained += len(report.Points)
		partial = partial || report.SourceTruncated
	}
	request, _ := http.NewRequestWithContext(r.Context(), http.MethodGet, "http://broker/recordings/track?"+q.Encode(), nil)
	response, e := s.client.Do(request)
	liveOK := false
	if e == nil {
		defer response.Body.Close()
		var value struct {
			Data trips.TrackReport `json:"data"`
		}
		if response.StatusCode == 200 && json.NewDecoder(io.LimitReader(response.Body, 32*1024*1024+1)).Decode(&value) == nil && value.Data.Points != nil {
			// A route displayed today must still be available after the recorder
			// overwrites these clips or the process restarts. Save every measured
			// point before serving it; the periodic scan alone leaves a gap.
			if _, e := s.tracks.Ingest(value.Data, now); e != nil {
				s.loggerMu.Lock()
				s.logger.TrackRetentionError = "track-retention-unavailable"
				s.loggerMu.Unlock()
				fail(w, 503, "TRACK_RETENTION_UNAVAILABLE", "GPS経路の保存に失敗しました")
				return
			}
			samples = append(samples, trips.TrackSamples(value.Data)...)
			partial = partial || value.Data.SourceTruncated
			liveOK = true
		}
	}
	// A radio log may retain isolated measured fixes after its NMEA was deleted.
	// Show these as points only, never as an invented continuous route.
	if len(samples) == 0 && s.radioStore != nil {
		located, e := s.radioStore.Query(radio.Query{From: from, To: to, IncludeUnknownAccuracy: true})
		if e == nil {
			for _, point := range located.Points {
				p := point.GPS
				samples = append(samples, trips.Sample{At: p.At, Lat: p.Lat, Lon: p.Lon, Valid: true, Quality: 1, HDOP: p.HDOP, AccuracyM: p.AccuracyM, Source: p.Source, BreakBefore: true})
			}
			retained += len(located.Points)
			partial = partial || len(located.Points) > 0
		}
	}
	var track trips.TrackReport
	snapshot := trips.Snapshot{Samples: samples, Truncated: partial, IndexedAt: now}
	if date != "" {
		track, err = trips.DayTrack(snapshot, date, now)
	} else {
		track, err = trips.WindowTrack(snapshot, from, to, now)
	}
	if err != nil {
		fail(w, 400, "INVALID_TRACK_RANGE", "地図の時刻範囲を確認してください")
		return
	}
	if !liveOK && len(track.Points) == 0 {
		fail(w, 503, "TRACK_SOURCE_UNAVAILABLE", "GPS記録を取得できません")
		return
	}
	if retained > 0 {
		track.Source = "ncc-retained-measured-positions"
	}
	jsonOut(w, 200, model.Value(track.Source, track))
}
