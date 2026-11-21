package trips

import (
	"math"
	"testing"
	"time"
)

func TestCourseBeforeUsesApproachWhileStopped(t *testing.T) {
	at := time.Now()
	points := []TrackPoint{
		{At: at.Add(-12 * time.Second), Lat: 10, Lon: 10},
		{At: at.Add(-10 * time.Second), Lat: 10, Lon: 10.00010},
		{At: at.Add(-8 * time.Second), Lat: 10, Lon: 10.00020},
		{At: at.Add(-6 * time.Second), Lat: 10, Lon: 10.00029},
		{At: at.Add(-4 * time.Second), Lat: 10.00002, Lon: 10.00029, SpeedMPS: ptr(0)},
		{At: at.Add(-2 * time.Second), Lat: 9.99999, Lon: 10.00029, SpeedMPS: ptr(0)},
		{At: at, Lat: 10, Lon: 10.00029, SpeedMPS: ptr(0)},
	}
	if got := CourseBefore(points, at); got == nil || math.Abs(*got-90) > 2 {
		t.Fatalf("expected east from the approach, got %v", got)
	}
	if got := CourseBefore(points[4:], at); got != nil {
		t.Fatal("stationary GPS fixes produced a direction")
	}
	if got := CourseBefore(points, at.Add(6*time.Second)); got != nil {
		t.Fatal("stale fixes produced a direction")
	}
	longStop := append([]TrackPoint{}, points...)
	for n := 2; n <= 90; n += 2 {
		longStop = append(longStop, TrackPoint{At: at.Add(time.Duration(n) * time.Second), Lat: 10, Lon: 10.00029})
	}
	if got := CourseBefore(longStop, longStop[len(longStop)-1].At); got == nil || math.Abs(*got-90) > 2 {
		t.Fatal("the approach was lost while waiting at the signal")
	}
	for i := 4; i < len(points); i++ {
		points[i].Segment++
	}
	if got := CourseBefore(points, at); got != nil {
		t.Fatal("a prior segment was joined across a GPS gap")
	}
}
