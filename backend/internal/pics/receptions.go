package pics

import "time"

// Reception holds the received state and durations measured on uninterrupted
// advertisements. A cycle is measured between two observed red-to-green edges.
type Reception struct {
	At               time.Time    `json:"at"`
	ID               string       `json:"id"`
	Name             string       `json:"name,omitempty"`
	Slot             int          `json:"slot"`
	Color            Color        `json:"color"`
	Step             int          `json:"step,omitempty"`
	Beacon           *Coordinates `json:"beacon,omitempty"`
	PreviousColor    Color        `json:"previousColor,omitempty"`
	PhaseSeconds     float64      `json:"phaseSeconds,omitempty"`
	CycleSeconds     float64      `json:"cycleSeconds,omitempty"`
	EstimatedToGreen int          `json:"estimatedToGreen,omitempty"`
}

func (t *timing) reception(previous Lamp, gap time.Duration, blue bool, now time.Time) (Reception, bool) {
	record := Reception{At: now.UTC(), Color: t.lamp.Color, Step: t.lamp.Step, EstimatedToGreen: t.seconds(now)}
	if gap > freshFor || previous.Color == "" || t.lamp.Color == "" {
		t.colorSince, t.cycleStart = time.Time{}, time.Time{}
	} else if previous.Color != t.lamp.Color {
		if duration := now.Sub(t.colorSince); duration > 0 && duration <= 10*time.Minute {
			record.PreviousColor, record.PhaseSeconds = previous.Color, duration.Seconds()
		}
		t.colorSince = now
		if blue {
			if duration := now.Sub(t.cycleStart); duration > 0 && duration <= 10*time.Minute {
				record.CycleSeconds = duration.Seconds()
			}
			t.cycleStart = now
		}
	}
	changed := gap > freshFor || previous != t.lamp
	if t.lamp.Color == "" || !changed && now.Sub(t.lastRecorded) < 15*time.Second {
		return Reception{}, false
	}
	t.lastRecorded = now
	return record, true
}
