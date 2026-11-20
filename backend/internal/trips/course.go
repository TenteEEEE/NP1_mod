package trips

import (
	"math"
	"sort"
	"time"
)

const CourseHistory = 3 * time.Minute

// CourseBefore estimates the approach using three consistent measured points
// in the latest segment. A stationary point by itself never gives a course.
func CourseBefore(points []TrackPoint, at time.Time) *float64 {
	end := sort.Search(len(points), func(i int) bool { return points[i].At.After(at) }) - 1
	if end < 2 || at.Sub(points[end].At) > 5*time.Second {
		return nil
	}
	segment := points[end].Segment
	initial := end
	for i := end - 1; i >= 0 && points[i].Segment == segment && trackMetres(points[i], points[end]) <= 5; i-- {
		initial = i
	}
	if points[end].At.Sub(points[initial].At) >= 4*time.Second {
		end = initial
	}
	for i := end; i >= 2 && points[i].Segment == segment && at.Sub(points[i].At) <= CourseHistory; i-- {
		current := points[i]
		if current.SpeedMPS != nil && *current.SpeedMPS < 1.5 {
			continue
		}
		for j := i - 2; j >= 0 && points[j].Segment == segment && current.At.Sub(points[j].At) <= 12*time.Second; j-- {
			first, middle := points[j], points[(i+j)/2]
			distance, elapsed := trackMetres(first, current), current.At.Sub(first.At).Seconds()
			if distance < 16 || elapsed < 2 || distance > elapsed*50 || trackMetres(first, middle) < 6 || trackMetres(middle, current) < 6 {
				continue
			}
			if d := math.Abs(trackBearing(first, middle) - trackBearing(middle, current)); math.Min(d, 360-d) <= 35 {
				return ptr(trackBearing(first, current))
			}
		}
	}
	return nil
}

func trackMetres(a, b TrackPoint) float64 {
	return metres(Sample{Lat: a.Lat, Lon: a.Lon}, Sample{Lat: b.Lat, Lon: b.Lon})
}

func trackBearing(a, b TrackPoint) float64 {
	const radians = math.Pi / 180
	lat1, lat2 := a.Lat*radians, b.Lat*radians
	dlon := (b.Lon - a.Lon) * radians
	y := math.Sin(dlon) * math.Cos(lat2)
	x := math.Cos(lat1)*math.Sin(lat2) - math.Sin(lat1)*math.Cos(lat2)*math.Cos(dlon)
	return math.Mod(360+math.Atan2(y, x)/radians, 360)
}
