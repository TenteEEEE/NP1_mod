package trips

import (
	"sort"
	"time"
)

// RollingHistory clips retained measured runs to an elapsed 24-hour window.
// Hourly bars distribute each measured run over its observed duration. Only a
// run crossing the window edge requires an estimate for the overall total.
func (a *Archive) RollingHistory(now time.Time) (HistoryReport, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.load(); err != nil {
		return HistoryReport{}, err
	}
	start := now.Add(-24 * time.Hour)
	out, err := History(Snapshot{}, HistoryOptions{Period: "24h"}, now)
	if err != nil {
		return out, err
	}
	out.Range.FromAt = start.UTC().Format(time.RFC3339Nano)
	out.Range.ToAt = now.UTC().Format(time.RFC3339Nano)
	out.Source = "ncc-retained-measured-intervals"
	out.SourceFiles = a.data.Health.Files
	out.NoFixSentences = a.data.Health.NoFix
	out.BadChecksums = a.data.Health.BadChecksum
	out.UnreadableFiles = a.data.Health.UnreadableFiles
	out.IndexedAt = a.data.Health.IndexedAt
	out.SourceTruncated = a.data.SourcePartial || a.data.LimitReached || a.data.OverlapRejected > 0 || a.data.Backfilling
	for i := 0; i < 24; i++ {
		out.Hours = append(out.Hours, HistoryHour{From: start.Add(time.Duration(i) * time.Hour).UTC(), To: start.Add(time.Duration(i+1) * time.Hour).UTC()})
	}
	dayIndex := map[string]int{}
	for i, day := range out.Days {
		dayIndex[day.Date] = i
	}
	trips := map[string]*Trip{}
	distance, driving := 0.0, 0.0
	for _, day := range a.data.Days {
		for _, run := range day.Runs {
			if run.End.After(now) || !run.End.After(start) {
				continue
			}
			from, to := run.Start, run.End
			if from.Before(start) {
				from = start
				out.BoundaryEstimated = true
			}
			seconds := to.Sub(from).Seconds()
			total := run.End.Sub(run.Start).Seconds()
			if seconds <= 0 || total <= 0 {
				continue
			}
			fraction := seconds / total
			d, h := run.DistanceKM*fraction, run.DrivingSeconds*fraction
			distance += d
			driving += h
			out.Period.HasData = true
			if i, ok := dayIndex[day.Date]; ok {
				item := &out.Days[i]
				if !item.Observed {
					item.Observed = true
					item.DistanceKM = ptr(0)
					item.DrivingSeconds = ptr(0)
					item.Reason = ""
				}
				*item.DistanceKM += d
				*item.DrivingSeconds += h
				item.ObservedSeconds += seconds
				item.SampleCount += int(float64(run.Samples) * fraction)
			}
			trip := trips[run.ID]
			if trip == nil {
				trip = &Trip{ID: run.ID, Start: from}
				trips[run.ID] = trip
			}
			trip.End = to
			trip.DistanceKM += d
			trip.DrivingSeconds += h
			trip.ObservedSeconds += seconds
			for i := range out.Hours {
				bucket := &out.Hours[i]
				left, right := from, to
				if left.Before(bucket.From) {
					left = bucket.From
				}
				if right.After(bucket.To) {
					right = bucket.To
				}
				if !left.Before(right) {
					continue
				}
				if !bucket.Observed {
					bucket.Observed = true
					bucket.DistanceKM = ptr(0)
					bucket.DrivingSeconds = ptr(0)
				}
				part := right.Sub(left).Seconds() / total
				*bucket.DistanceKM += run.DistanceKM * part
				*bucket.DrivingSeconds += run.DrivingSeconds * part
			}
		}
	}
	for _, day := range out.Days {
		if day.Observed {
			out.Period.ValidObservedDays++
		}
		out.Period.SampleCount += day.SampleCount
	}
	if out.Period.HasData {
		out.Period.DistanceKM = &distance
		out.Period.DrivingSeconds = &driving
		out.Status = "available"
		out.Reason = ""
	}
	out.Period.Days = out.Days
	for _, trip := range trips {
		out.Trips = append(out.Trips, *trip)
	}
	sort.Slice(out.Trips, func(i, j int) bool { return out.Trips[i].Start.After(out.Trips[j].Start) })
	out.TotalTrips = len(out.Trips)
	if len(out.Trips) > MaximumHistoryTrips {
		out.Trips = out.Trips[:MaximumHistoryTrips]
		out.DetailsTruncated = true
	}
	return out, nil
}
