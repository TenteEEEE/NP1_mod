package trips

import (
	"compress/gzip"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"time"
)

// TrackStore retains measured coordinates independently of SD loop recording.
// It never modifies the aggregate journey archive or interpolates missing GPS.
type TrackStore struct {
	directory string
	mu        sync.Mutex
}

func NewTrackStore(state string) *TrackStore {
	return &TrackStore{directory: filepath.Join(state, "journey-tracks")}
}

func TrackSamples(report TrackReport) []Sample {
	out := make([]Sample, 0, len(report.Points))
	for i, p := range report.Points {
		s := Sample{At: p.At, Lat: p.Lat, Lon: p.Lon, Valid: true, Quality: 1, Source: report.Source, SpeedMPS: p.SpeedMPS, Heading: p.Heading, AccuracyM: p.AccuracyM, HDOP: p.HDOP}
		if i > 0 && p.Segment != report.Points[i-1].Segment {
			s.BreakBefore = true
		}
		if Usable(s) {
			out = append(out, s)
		}
	}
	return out
}
func (s *TrackStore) read(date string) (TrackReport, error) {
	if _, err := TrackDate(date); err != nil {
		return TrackReport{}, err
	}
	f, err := os.Open(filepath.Join(s.directory, date+".json.gz"))
	if errors.Is(err, os.ErrNotExist) {
		return TrackReport{Date: date, Points: []TrackPoint{}, Segments: []TrackSegment{}}, nil
	}
	if err != nil {
		return TrackReport{}, err
	}
	defer f.Close()
	z, err := gzip.NewReader(f)
	if err != nil {
		return TrackReport{}, err
	}
	defer z.Close()
	var report TrackReport
	if err = json.NewDecoder(io.LimitReader(z, 32*1024*1024+1)).Decode(&report); err != nil {
		return report, err
	}
	if report.Date != date || len(report.Points) > MaximumIndexedSamples {
		return report, errors.New("invalid retained GPS track")
	}
	return report, nil
}
func (s *TrackStore) Read(date string) (TrackReport, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.read(date)
}

// Ingest saves whole JST days, merging by measurement timestamp. Repeated scans
// leave identical files untouched. No automatic deletion of historical fixes.
func (s *TrackStore) Ingest(report TrackReport, now time.Time) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	days := map[string][]Sample{}
	for _, sample := range TrackSamples(report) {
		if !sample.At.After(now) {
			key := sample.At.In(Japan).Format("2006-01-02")
			days[key] = append(days[key], sample)
		}
	}
	added := 0
	for date, points := range days {
		old, err := s.read(date)
		if err != nil {
			return added, err
		}
		all := ordered(append(TrackSamples(old), points...))
		merged, err := DayTrack(Snapshot{Samples: all, Truncated: old.SourceTruncated || report.SourceTruncated, IndexedAt: now}, date, now)
		if err != nil {
			return added, err
		}
		merged.Source = "ncc-retained-measured-positions"
		if len(merged.Points) > MaximumIndexedSamples {
			return added, errors.New("daily GPS retention limit; existing records preserved")
		}
		if reflect.DeepEqual(old.Points, merged.Points) && old.SourceTruncated == merged.SourceTruncated {
			continue
		}
		if err = os.MkdirAll(s.directory, 0700); err != nil {
			return added, err
		}
		entries, err := os.ReadDir(s.directory)
		if err != nil {
			return added, err
		}
		var total int64
		for _, entry := range entries {
			if info, e := entry.Info(); e == nil {
				total += info.Size()
			}
		}
		if total > 128*1024*1024 {
			return added, errors.New("retained GPS storage full; existing records preserved")
		}
		f, err := os.CreateTemp(s.directory, ".track-*")
		if err != nil {
			return added, err
		}
		name := f.Name()
		z := gzip.NewWriter(f)
		err = json.NewEncoder(z).Encode(merged)
		if closeErr := z.Close(); err == nil {
			err = closeErr
		}
		if err == nil {
			err = f.Sync()
		}
		if e := f.Close(); err == nil {
			err = e
		}
		if err == nil {
			err = os.Rename(name, filepath.Join(s.directory, date+".json.gz"))
		}
		os.Remove(name)
		if err != nil {
			return added, err
		}
		check, err := s.read(date)
		if err != nil || !reflect.DeepEqual(check.Points, merged.Points) {
			return added, errors.New("retained GPS read-back failed")
		}
		added += len(merged.Points) - len(old.Points)
	}
	return added, nil
}
