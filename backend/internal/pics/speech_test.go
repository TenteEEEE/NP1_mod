package pics

import "testing"

func TestAllPICSAnnouncementsAreCoveredByTheCache(t *testing.T) {
	prepared := map[string]bool{}
	for _, text := range Vocabulary() {
		prepared[text] = true
	}
	cues := []Cue{{Kind: "green"}, {Kind: otherBlinkCue}, {Kind: soonCue}}
	for n := 5; n >= 1; n-- {
		cues = append(cues, Cue{Kind: "number", Seconds: n})
	}
	for _, cue := range cues {
		for _, text := range SpeechParts(cue) {
			if !prepared[text] {
				t.Fatalf("missing cached cue %v: %q", cue, text)
			}
		}
	}
	for n := 1; n <= maxSeconds; n++ {
		parts := SpeechParts(Cue{Kind: "remaining", Seconds: n})
		if len(parts) < 2 {
			t.Fatalf("missing time phrase for %d seconds", n)
		}
		for _, text := range parts {
			if !prepared[text] {
				t.Fatalf("missing cached clip for %d seconds: %q", n, text)
			}
		}
	}
}
