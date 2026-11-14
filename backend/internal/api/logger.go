package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"github.com/np1-control-center/ncc/internal/config"
	"github.com/np1-control-center/ncc/internal/model"
	"github.com/np1-control-center/ncc/internal/radio"
	"github.com/np1-control-center/ncc/internal/trips"
)

type LoggerState struct {
	Enabled        bool      `json:"enabled"`
	Active         bool      `json:"active"`
	Stored         uint64    `json:"stored"`
	Located        uint64    `json:"located"`
	LastObservedAt time.Time `json:"lastObservedAt"`
	LastStoredAt   time.Time `json:"lastStoredAt"`
	Error          string    `json:"error,omitempty"`
	LastRepairAt   time.Time `json:"lastRepairAt,omitempty"`
	Repaired       uint64    `json:"repaired"`
	RepairError    string    `json:"repairError,omitempty"`
}

func (s *Server) loadLogger() {
	if bytes, e := os.ReadFile(filepath.Join(s.cfg.StateDir, "radio-logger.json")); e == nil && len(bytes) <= 2048 {
		_ = json.Unmarshal(bytes, &s.logger)
	}
	// The owner explicitly requested continuous logging without a consent gate.
	s.logger.Enabled = true
	s.logger.Active = false
}
func (s *Server) loggerStatus(w http.ResponseWriter, r *http.Request) {
	s.loggerMu.Lock()
	defer s.loggerMu.Unlock()
	jsonOut(w, 200, model.Value("continuous observation logger", s.logger))
}
func (s *Server) storeRadio(sample radio.Observation, now time.Time) error {
	s.loggerMu.Lock()
	defer s.loggerMu.Unlock()
	if sample.At.IsZero() || sample.Source == "" || sample.At.After(now.Add(2*time.Second)) || now.Sub(sample.At) > 2*time.Minute {
		return nil
	}
	if !sample.At.After(s.logger.LastObservedAt) {
		return nil
	}
	if e := s.radioStore.Append(sample); e != nil {
		s.logger.Error = "storage-unavailable"
		return e
	}
	s.logger.LastObservedAt = sample.At
	s.logger.LastStoredAt = now
	s.logger.Stored++
	s.logger.Error = ""
	if sample.GPS != nil && sample.GPS.At.Sub(sample.At).Abs() <= 5*time.Second {
		s.logger.Located++
	}
	return config.Atomic(filepath.Join(s.cfg.StateDir, "radio-logger.json"), s.logger)
}
func (s *Server) collectRadio(ctx context.Context) {
	s.loggerMu.Lock()
	s.logger.Enabled = true
	s.logger.Active = true
	s.loggerMu.Unlock()
	defer func() { s.loggerMu.Lock(); s.logger.Active = false; s.loggerMu.Unlock() }()
	var nextRepair time.Time
	collect := func() {
		// Retry after clips close even if the current modem sample is missing.
		defer func() {
			now := time.Now().UTC()
			if !now.Before(nextRepair) {
				nextRepair = now.Add(time.Minute)
				s.repairRecentRadio(ctx, now)
			}
		}()
		request, e := http.NewRequestWithContext(ctx, "GET", "http://broker/radio-sample", nil)
		if e != nil {
			return
		}
		response, e := s.client.Do(request)
		if e != nil {
			return
		}
		defer response.Body.Close()
		var result struct {
			Status string            `json:"status"`
			Data   radio.Observation `json:"data"`
		}
		if response.StatusCode != 200 || json.NewDecoder(io.LimitReader(response.Body, 16*1024)).Decode(&result) != nil || result.Status != "available" {
			return
		}
		_ = s.storeRadio(result.Data, time.Now().UTC())
	}
	collect()
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			collect()
		}
	}
}

func (s *Server) repairRecentRadio(ctx context.Context, now time.Time) {
	query := url.Values{"from": {now.Add(-15 * time.Minute).Format(time.RFC3339Nano)}, "to": {now.Format(time.RFC3339Nano)}}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	updated, err := s.matchRadioPositions(ctx, query)
	s.loggerMu.Lock()
	defer s.loggerMu.Unlock()
	s.logger.LastRepairAt = now
	s.logger.RepairError = ""
	s.logger.Repaired += uint64(updated)
	if err != nil {
		s.logger.RepairError = "position-repair-unavailable"
	}
}

func (s *Server) matchRadioPositions(ctx context.Context, query url.Values) (int, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://broker/recordings/track?"+query.Encode(), nil)
	if err != nil {
		return 0, err
	}
	response, err := s.client.Do(request)
	if err != nil {
		return 0, err
	}
	defer response.Body.Close()
	var value struct {
		Data trips.TrackReport `json:"data"`
	}
	if response.StatusCode != 200 {
		return 0, fmt.Errorf("track status %d", response.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, 4*1024*1024+1))
	if err != nil {
		return 0, err
	}
	if len(raw) > 4*1024*1024 || json.Unmarshal(raw, &value) != nil || value.Data.Points == nil {
		return 0, errors.New("invalid radio positions")
	}
	positions := make([]radio.Position, 0, len(value.Data.Points))
	for _, p := range value.Data.Points {
		positions = append(positions, radio.Position{At: p.At, Lat: p.Lat, Lon: p.Lon, AccuracyM: p.AccuracyM, HDOP: p.HDOP, Source: value.Data.Source})
	}
	if len(positions) == 0 {
		return 0, nil
	}
	report, err := s.radioStore.RepairPositions(positions, true)
	return report.Updated, err
}
