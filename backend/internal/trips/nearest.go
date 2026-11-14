package trips

import (
	"sort"
	"time"
)

// NearestSamples returns, for each time, the usable sample closest to it within tolerance
// (nil when there is none). The recorder logs one sample per second while it has a fix.
func NearestSamples(samples []Sample, at []time.Time, tolerance time.Duration) []*Sample {
	usable := make([]Sample, 0, len(samples))
	for _, s := range ordered(samples) {
		if Usable(s) {
			usable = append(usable, s)
		}
	}
	out := make([]*Sample, len(at))
	for i, t := range at {
		j := sort.Search(len(usable), func(k int) bool { return !usable[k].At.Before(t) })
		var best *Sample
		for _, k := range []int{j - 1, j} {
			if k < 0 || k >= len(usable) {
				continue
			}
			d := usable[k].At.Sub(t)
			if d < 0 {
				d = -d
			}
			if d <= tolerance && (best == nil || d < absDiff(best.At, t)) {
				copy := usable[k]
				best = &copy
			}
		}
		out[i] = best
	}
	return out
}

func absDiff(a, b time.Time) time.Duration {
	if d := a.Sub(b); d >= 0 {
		return d
	}
	return b.Sub(a)
}
