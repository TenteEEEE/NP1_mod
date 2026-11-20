package api

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/np1-control-center/ncc/internal/pics"
	"github.com/np1-control-center/ncc/internal/trips"
)

const picsDayLimit = 2 << 20

type picsReception struct {
	pics.Reception
	Position *pics.Coordinates `json:"position,omitempty"`
	Course   *float64          `json:"course,omitempty"`
}

func (p *picsService) recordPICS(records []pics.Reception, now time.Time) {
	if len(records) == 0 {
		return
	}
	var position *pics.Coordinates
	if n := len(p.positions); n > 0 && p.positions[n-1].At.Sub(now).Abs() <= 3*time.Second {
		latest := p.positions[n-1]
		position = &pics.Coordinates{Lat: latest.Lat, Lon: latest.Lon}
	}
	batch := make([]picsReception, 0, len(records))
	for _, record := range records {
		batch = append(batch, picsReception{Reception: record, Position: position, Course: p.currentPICSCourse(now)})
	}
	select {
	case p.receptions <- batch:
	default:
		p.historyIncomplete = true
	}
}

func (p *picsService) collectPICSHistory(ctx context.Context) {
	timer := time.NewTicker(time.Second)
	defer timer.Stop()
	days := map[string][]picsReception{}
	flush := func() {
		for day, records := range days {
			if err := appendPICSHistory(p.historyDir, day, records); err != nil {
				p.mu.Lock()
				p.historyIncomplete = true
				p.mu.Unlock()
			}
			delete(days, day)
		}
	}
	add := func(batch []picsReception) {
		day := batch[0].At.In(trips.Japan).Format("2006-01-02")
		days[day] = append(days[day], batch...)
		if len(days[day]) >= 128 {
			flush()
		}
	}
	for {
		select {
		case <-ctx.Done():
			for {
				select {
				case batch := <-p.receptions:
					add(batch)
				default:
					flush()
					return
				}
			}
		case <-timer.C:
			flush()
		case batch := <-p.receptions:
			add(batch)
		}
	}
}

func appendPICSHistory(directory, day string, records []picsReception) error {
	var data bytes.Buffer
	// Start at a new line even after an interrupted append.
	data.WriteByte('\n')
	encoder := json.NewEncoder(&data)
	for _, record := range records {
		if err := encoder.Encode(record); err != nil {
			return err
		}
	}
	if err := os.MkdirAll(directory, 0700); err != nil {
		return err
	}
	path := filepath.Join(directory, day+".jsonl")
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		entries, err := os.ReadDir(directory)
		if err != nil {
			return err
		}
		var total int64
		for _, entry := range entries {
			if info, err := entry.Info(); err == nil {
				total += info.Size()
			}
		}
		if total >= 48<<20 {
			return errors.New("PICS history storage is full")
		}
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return err
	}
	if st.Size() >= picsDayLimit {
		return errors.New("PICS daily history storage is full")
	}
	if _, err := f.Write(data.Bytes()); err != nil {
		return err
	}
	return f.Sync()
}

func readPICSHistory(path string) ([]picsReception, bool, error) {
	result := []picsReception{}
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return result, false, nil
	}
	if err != nil {
		return result, false, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return result, false, err
	}
	partial := st.Size() >= picsDayLimit
	limit := min(st.Size(), picsDayLimit+128*1024)
	scanner := bufio.NewScanner(io.LimitReader(f, limit))
	for scanner.Scan() {
		if len(scanner.Bytes()) == 0 {
			continue
		}
		var record picsReception
		if json.Unmarshal(scanner.Bytes(), &record) == nil && !record.At.IsZero() && record.ID != "" {
			result = append(result, record)
		} else {
			partial = true
		}
	}
	return result, partial, scanner.Err()
}

func (s *Server) picsHistory(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	date := q.Get("date")
	if _, err := trips.TrackDate(date); err != nil || len(q) != 1 || len(q["date"]) != 1 {
		fail(w, 400, "INVALID_PICS_HISTORY_DATE", "受信記録の日付を確認してください")
		return
	}
	entries, incomplete, err := readPICSHistory(filepath.Join(s.pics.historyDir, date+".jsonl"))
	if err != nil {
		fail(w, 503, "PICS_HISTORY_UNAVAILABLE", "高度化PICSの受信記録を読み出せません")
		return
	}
	// Recorded NMEA becomes available after the recorder closes the segment.
	if len(entries) > 0 {
		if track, err := s.tracks.Read(date); err == nil {
			fillPICSPositions(entries, track)
		}
	}
	s.pics.mu.Lock()
	incomplete = incomplete || s.pics.historyIncomplete
	s.pics.mu.Unlock()
	w.Header().Set("Cache-Control", "no-store")
	jsonOut(w, 200, map[string]any{"date": date, "entries": entries, "incomplete": incomplete})
}

func fillPICSPositions(entries []picsReception, track trips.TrackReport) {
	lookups := []time.Time{}
	indices := []int{}
	for i, record := range entries {
		if record.Position == nil || record.Course == nil {
			lookups = append(lookups, record.At)
			indices = append(indices, i)
		}
	}
	if len(lookups) == 0 {
		return
	}
	for i, sample := range trips.NearestSamples(trips.TrackSamples(track), lookups, 3*time.Second) {
		record := &entries[indices[i]]
		if record.Position == nil && sample != nil {
			record.Position = &pics.Coordinates{Lat: sample.Lat, Lon: sample.Lon}
		}
		if record.Course == nil {
			record.Course = trips.CourseBefore(track.Points, record.At)
		}
	}
}
