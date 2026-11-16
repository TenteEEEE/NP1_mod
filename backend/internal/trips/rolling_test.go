package trips

import (
	"testing"
	"time"
)

func TestRollingHistoryIncludesYesterdayAfterCutoffAndCrossesMidnight(t *testing.T) {
	now := time.Date(2026, 11, 15, 8, 30, 0, 0, Japan)
	start := now.Add(-24 * time.Hour)
	a := NewArchive(t.TempDir())
	batch := intervalBatch(testInterval(start.Add(-time.Minute), 1, .009, 1), testInterval(start.Add(time.Minute), 1, .01, 1), testInterval(time.Date(2026, 11, 14, 23, 59, 59, 0, Japan), 2, .02, 2), testInterval(now.Add(-time.Minute), 1, .03, 1))
	if _, err := a.Ingest(batch, now); err != nil {
		t.Fatal(err)
	}
	report, err := a.RollingHistory(now)
	if err != nil || !report.Period.HasData || len(report.Hours) != 24 {
		t.Fatal(report, err)
	}
	nearly(t, *report.Period.DistanceKM, .06)
	nearly(t, *report.Period.DrivingSeconds, 4)
	if report.Range.FromAt != start.UTC().Format(time.RFC3339Nano) || report.Range.ToAt != now.UTC().Format(time.RFC3339Nano) {
		t.Fatal(report.Range)
	}
	var sum float64
	for _, h := range report.Hours {
		if h.DistanceKM != nil {
			sum += *h.DistanceKM
		}
	}
	nearly(t, sum, .06)
}
