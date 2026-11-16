package trips

import (
	"errors"
	"time"
)

type TrackPoint struct {
	At        time.Time `json:"at"`
	Lat       float64   `json:"lat"`
	Lon       float64   `json:"lon"`
	Segment   int       `json:"segment"`
	SpeedMPS  *float64  `json:"speedMps,omitempty"`
	Heading   *float64  `json:"heading,omitempty"`
	AccuracyM *float64  `json:"accuracyM,omitempty"`
	HDOP      *float64  `json:"hdop,omitempty"`
}
type TrackSegment struct {
	ID             int       `json:"id"`
	Start          time.Time `json:"start"`
	End            time.Time `json:"end"`
	PointCount     int       `json:"pointCount"`
	DistanceKM     *float64  `json:"distanceKm"`
	DrivingSeconds *float64  `json:"drivingSeconds"`
}
type TrackReport struct {
	Date            string         `json:"date"`
	From            *time.Time     `json:"from,omitempty"`
	To              *time.Time     `json:"to,omitempty"`
	Points          []TrackPoint   `json:"points"`
	Segments        []TrackSegment `json:"segments"`
	Source          string         `json:"source"`
	SourceTruncated bool           `json:"sourceTruncated"`
	IndexedAt       time.Time      `json:"indexedAt"`
	Reason          string         `json:"reason,omitempty"`
}

func TrackDate(date string) (time.Time, error) {
	day, err := time.ParseInLocation("2006-01-02", date, Japan)
	if err != nil || day.Format("2006-01-02") != date {
		return time.Time{}, errors.New("INVALID_DATE")
	}
	return day, nil
}

// DayTrack exposes measured coordinates separately from aggregate statistics.
// Invalid fixes, missing intervals and unrealistic jumps never become lines.
func DayTrack(snapshot Snapshot, date string, now time.Time) (TrackReport, error) {
	day, err := TrackDate(date)
	if err != nil {
		return TrackReport{}, err
	}
	out := trackBetween(snapshot, day, day.AddDate(0, 0, 1), now, false)
	out.Date = date
	if len(out.Points) == 0 {
		out.Reason = "この日の録画に有効な位置記録はありません"
	}
	return out, nil
}

// WindowTrack uses an elapsed-time window, independent of JST midnight. Both
// endpoints are included so a fix exactly at the requested end remains visible.
func WindowTrack(snapshot Snapshot, from, to, now time.Time) (TrackReport, error) {
	if from.IsZero() || to.IsZero() || !from.Before(to) || to.Sub(from) > 24*time.Hour {
		return TrackReport{}, errors.New("INVALID_TRACK_RANGE")
	}
	out := trackBetween(snapshot, from, to, now, true)
	from, to = from.UTC(), to.UTC()
	out.From, out.To = &from, &to
	return out, nil
}

func trackBetween(snapshot Snapshot, from, end, now time.Time, includeEnd bool) TrackReport {
	out := TrackReport{Points: []TrackPoint{}, Segments: []TrackSegment{}, Source: "stock-recording-nmea", SourceTruncated: snapshot.Truncated, IndexedAt: snapshot.IndexedAt}
	var previous *Sample
	for _, sample := range ordered(snapshot.Samples) {
		if sample.At.Before(from) || sample.At.After(end) || (!includeEnd && sample.At.Equal(end)) || sample.At.After(now) || !Usable(sample) {
			previous = nil
			continue
		}
		var segment *TrackSegment
		if previous != nil {
			if distance, driving, _, ok := interval(*previous, sample); ok {
				segment = &out.Segments[len(out.Segments)-1]
				if segment.DistanceKM == nil {
					segment.DistanceKM = ptr(0)
					segment.DrivingSeconds = ptr(0)
				}
				*segment.DistanceKM += distance
				*segment.DrivingSeconds += driving
			}
		}
		if segment == nil {
			out.Segments = append(out.Segments, TrackSegment{ID: len(out.Segments), Start: sample.At.UTC()})
			segment = &out.Segments[len(out.Segments)-1]
		}
		segment.End = sample.At.UTC()
		segment.PointCount++
		out.Points = append(out.Points, TrackPoint{At: sample.At.UTC(), Lat: sample.Lat, Lon: sample.Lon, Segment: segment.ID, SpeedMPS: sample.SpeedMPS, Heading: sample.Heading, AccuracyM: sample.AccuracyM, HDOP: sample.HDOP})
		copy := sample
		previous = &copy
	}
	if len(out.Points) == 0 {
		out.Reason = "この期間の録画に有効な位置記録はありません"
	}
	return out
}
