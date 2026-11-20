package pics

import (
	"cmp"
	"slices"
	"time"
)

type Signal struct {
	ID      string `json:"id"`
	Name    string `json:"name,omitempty"`
	Slot    int    `json:"slot"`
	Color   Color  `json:"color"`
	Seconds *int   `json:"seconds"`
}

type Monitor struct {
	intersections map[string]*intersection
	sequence      uint64
	lastBlue      time.Time
	focus         Cue
}

type intersection struct {
	name, source                  string
	lastSeen, lastType2, conflict time.Time
	lamps                         [6]timing
	blue                          [6]time.Time
}

func (m *Monitor) Receive(source string, payload []byte, now time.Time) {
	p, ok := parse(payload)
	if !ok {
		return
	}
	if m.intersections == nil {
		m.intersections = make(map[string]*intersection)
	}
	crossing := m.intersections[p.id]
	if crossing == nil {
		if len(m.intersections) >= 100 {
			var oldest string
			for id, candidate := range m.intersections {
				if oldest == "" || candidate.lastSeen.Before(m.intersections[oldest].lastSeen) {
					oldest = id
				}
			}
			delete(m.intersections, oldest)
		}
		crossing = &intersection{}
		m.intersections[p.id] = crossing
	}
	crossing.lastSeen = now
	if p.kind == 0 {
		crossing.name = p.name
		return
	}
	if crossing.source != "" && crossing.source != source {
		if now.Sub(crossing.lastType2) <= freshFor {
			for i, lamp := range p.lamps {
				if lamp != crossing.lamps[i].lamp {
					crossing.conflict = now.Add(freshFor)
					crossing.lamps = [6]timing{}
					crossing.blue = [6]time.Time{}
					break
				}
			}
			return
		}
		crossing.lamps = [6]timing{}
		crossing.blue = [6]time.Time{}
	}
	if now.Before(crossing.conflict) {
		return
	}
	crossing.source, crossing.lastType2 = source, now
	m.sequence++
	for i, lamp := range p.lamps {
		if crossing.lamps[i].observe(lamp, now, m.sequence) {
			crossing.blue[i] = now
		}
	}
}

// Signals lists all recently seen lamps. A countdown is shown before color-only
// signals so that the compact dashboard can follow the next estimated change.
func (m *Monitor) Signals(now time.Time) []Signal {
	result := []Signal{}
	for id, crossing := range m.intersections {
		if now.Before(crossing.conflict) || now.Sub(crossing.lastType2) > showFor {
			continue
		}
		for i, t := range crossing.lamps {
			if t.lamp.Color == "" {
				continue
			}
			signal := Signal{ID: id, Name: crossing.name, Slot: i + 1, Color: t.lamp.Color}
			if n := t.seconds(now); n > 0 {
				signal.Seconds = &n
			}
			result = append(result, signal)
		}
	}
	slices.SortFunc(result, func(a, b Signal) int {
		if a.Seconds != nil && b.Seconds != nil {
			if order := cmp.Compare(*a.Seconds, *b.Seconds); order != 0 {
				return order
			}
		} else if a.Seconds != nil {
			return -1
		} else if b.Seconds != nil {
			return 1
		}
		if order := cmp.Compare(a.ID, b.ID); order != 0 {
			return order
		}
		return cmp.Compare(a.Slot, b.Slot)
	})
	return result
}

type Cue struct {
	Kind    string
	Seconds int
	id      string
	slot    int
	phase   uint64
	blueAt  time.Time
}

func (m *Monitor) NextCue(now time.Time) (Cue, bool) {
	signals := m.Signals(now)
	if now.Sub(m.lastBlue) >= time.Second {
		for _, signal := range signals {
			if signal.Color != Green {
				continue
			}
			at := m.intersections[signal.ID].blue[signal.Slot-1]
			if at.After(m.lastBlue) && now.Sub(at) <= freshFor {
				return Cue{Kind: "green", id: signal.ID, slot: signal.Slot - 1, blueAt: at}, true
			}
		}
	}
	if len(signals) == 0 || signals[0].Seconds == nil {
		return Cue{}, false
	}
	id, slot := signals[0].ID, signals[0].Slot-1
	// Continue a spoken countdown for the same lamp when estimates overlap.
	// All lamps still participate in the green-change detection above.
	if m.focus.id != "" && m.StillValid(m.focus, now) {
		id, slot = m.focus.id, m.focus.slot
	}
	t := &m.intersections[id].lamps[slot]
	n := t.seconds(now)
	cue := Cue{Kind: "remaining", Seconds: n, id: id, slot: slot, phase: t.phase}
	if n <= 5 && n < t.lastNumber {
		cue.Kind = "number"
		return cue, true
	}
	if n > 5 && (!t.announced || n == 10 && !t.ten) {
		return cue, true
	}
	return Cue{}, false
}

func (m *Monitor) Valid(cue Cue, now time.Time) bool {
	if !m.StillValid(cue, now) {
		return false
	}
	return cue.Kind == "green" || m.intersections[cue.id].lamps[cue.slot].seconds(now) == cue.Seconds
}

func (m *Monitor) StillValid(cue Cue, now time.Time) bool {
	crossing := m.intersections[cue.id]
	if crossing == nil || now.Before(crossing.conflict) {
		return false
	}
	t := &crossing.lamps[cue.slot]
	if cue.Kind == "green" {
		return cue.blueAt == crossing.blue[cue.slot] && t.lamp.Color == Green && now.Sub(t.at) <= freshFor
	}
	return t.phase == cue.phase && t.seconds(now) > 0
}

func (m *Monitor) Claim(cue Cue, now time.Time) {
	if cue.Kind == "green" {
		m.lastBlue = now
		return
	}
	m.focus = cue
	t := &m.intersections[cue.id].lamps[cue.slot]
	if cue.Kind == "number" {
		t.lastNumber = cue.Seconds
	} else {
		t.announced = true
		t.ten = cue.Seconds <= 10
	}
}
