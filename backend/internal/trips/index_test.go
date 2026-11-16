package trips

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSDSourceReadsEventAndParkingGNSSAlongsideNormalVideo(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{Directory, "DRIVERECDATA/Event", "DRIVERECDATA/Parking"} {
		if err := os.MkdirAll(filepath.Join(dir, filepath.FromSlash(name)), 0700); err != nil {
			t.Fatal(err)
		}
	}
	for name, body := range map[string]string{
		"DRIVERECDATA/Video/FILEM_261113_100000_0000.NMEA":   "GPRMC,010000.00,A,3500.0000,N,13900.0000,E,10.0,90.0,131126,,,A",
		"DRIVERECDATA/Event/EVENM_261114_100000_0000.NMEA":   "GPRMC,010000.00,A,3500.0000,N,13900.0000,E,10.0,90.0,141126,,,A",
		"DRIVERECDATA/Parking/PARKM_261114_100100_0000.NMEA": "GPRMC,010100.00,A,3500.0000,N,13900.0000,E,10.0,90.0,141126,,,A",
	} {
		if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(name)), []byte(Sentence(body)), 0600); err != nil {
			t.Fatal(err)
		}
	}
	source := SDSource{Root: dir, MountToken: func() (string, error) { return "fixture-card", nil }}
	snapshot, err := NewIndex(source).Read(context.Background())
	if err != nil || snapshot.Files != 3 || len(snapshot.Samples) != 3 {
		t.Fatal(snapshot, err)
	}
	track, err := DayTrack(snapshot, "2026-11-14", time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC))
	if err != nil || len(track.Points) != 2 {
		t.Fatal(track, err)
	}
	if _, err := source.Open("DRIVERECDATA/Event/../../secret.NMEA"); err == nil {
		t.Fatal("outside path accepted")
	}
}

type fixtureSource struct {
	token   string
	opens   int
	missing bool
}

type budgetSource struct {
	files []SourceFile
	opens int
}

func (s *budgetSource) List(context.Context) ([]SourceFile, string, error) {
	return s.files, "synthetic-budget-card", nil
}
func (s *budgetSource) Open(string) (io.ReadCloser, error) {
	s.opens++
	return io.NopCloser(strings.NewReader(Sentence("GPRMC,,V,,,,,,,,,,N"))), nil
}
func TestIndexBoundsSamplesAndCachedBackingArrays(t *testing.T) {
	source := &budgetSource{}
	index := NewIndex(source)
	index.token = "synthetic-budget-card"
	for file := 0; file < 25; file++ {
		metadata := SourceFile{Path: fmt.Sprintf("DRIVERECDATA/Video/FILEM_261111_120000_%04d.NMEA", file), Size: 100, Modified: time.Unix(0, 0)}
		source.files = append(source.files, metadata)
		samples := make([]Sample, 5010)
		for i := range samples {
			samples[i] = sample(time.Unix(int64(file*5010+i+1), 0), 35, 139, 0)
		}
		index.cache[metadata.Path] = cacheEntry{file: metadata, report: ParseReport{Samples: samples}}
	}
	snapshot, e := index.Read(context.Background())
	if e != nil || !snapshot.Truncated || len(snapshot.Samples) != MaximumIndexedSamples || source.opens != 0 {
		t.Fatal(len(snapshot.Samples), snapshot.Truncated, source.opens, e)
	}
	total := 0
	var clipped SourceFile
	for _, entry := range index.cache {
		total += len(entry.report.Samples)
		if entry.truncated {
			clipped = entry.file
		}
	}
	if total > MaximumIndexedSamples || clipped.Path == "" {
		t.Fatal("cached source exceeded sample budget", total, clipped)
	}
	// A clipped cache entry must be reparsed if it is the only remaining file.
	source.files = []SourceFile{clipped}
	index.at = time.Time{}
	snapshot, e = index.Read(context.Background())
	if e != nil || snapshot.Truncated || source.opens != 1 || len(snapshot.Samples) != 0 || snapshot.NoFix != 1 {
		t.Fatal(snapshot, source.opens, e)
	}
}

func (s *fixtureSource) Identity() (string, error) {
	if s.missing {
		return "", errors.New("card absent")
	}
	return s.token, nil
}
func (s *fixtureSource) List(context.Context) ([]SourceFile, string, error) {
	token, e := s.Identity()
	return []SourceFile{{Path: "DRIVERECDATA/Video/FILEM_261111_120000_0000.NMEA", Size: 100, Modified: time.Unix(0, 0)}}, token, e
}
func (s *fixtureSource) Open(string) (io.ReadCloser, error) {
	s.opens++
	return io.NopCloser(strings.NewReader(Sentence("GPRMC,,V,,,,,,,,,,N"))), nil
}
func TestIndexCacheMountRemovalAndReplacement(t *testing.T) {
	source := &fixtureSource{token: "synthetic-card-a"}
	index := NewIndex(source)
	ctx := context.Background()
	snapshot, e := index.Read(ctx)
	if e != nil || snapshot.Files != 1 || snapshot.NoFix != 1 {
		t.Fatal(snapshot, e)
	}
	index.Read(ctx)
	if source.opens != 1 {
		t.Fatal("cache missed", source.opens)
	}
	source.missing = true
	if _, e = index.Read(ctx); e == nil {
		t.Fatal("removed card reused cache")
	}
	source.missing = false
	source.token = "synthetic-card-b"
	if _, e = index.Read(ctx); e != nil || source.opens != 2 {
		t.Fatal("replacement did not invalidate", source.opens, e)
	}
}
