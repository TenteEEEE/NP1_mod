package platespeech

import (
	"context"
	"testing"
	"time"
)

func TestPreparedSpeechIsLocalAndDoesNotQueueBehindPlate(t *testing.T) {
	ctx := context.Background()
	synth, player := &fakeSynth{}, &fakePlayer{}
	m := NewManager(t.TempDir(), synth, player)
	m.pace = 0
	if err := m.SetAPI(DefaultAPIBase, ptr("test-key")); err != nil {
		t.Fatal(err)
	}
	texts := []string{"歩行者用信号", "27秒です。"}
	if err := m.Prepare(ctx, 3, texts); err != nil {
		t.Fatal(err)
	}
	if err := m.SetAPI(DefaultAPIBase, ptr("")); err != nil {
		t.Fatal(err)
	}
	allow := func(time.Duration) bool { return true }
	m.playback.Lock()
	err := m.PlayPrepared(ctx, 3, 60, texts, allow)
	m.playback.Unlock()
	if err == nil || len(player.played) != 0 {
		t.Fatal("prepared speech was not skipped when the speaker was busy")
	}
	if err := m.PlayPrepared(ctx, 3, 60, texts, allow); err != nil {
		t.Fatal(err)
	}
	if len(player.played) != 1 || len(synth.texts) != 2 {
		t.Fatalf("playback or synthesis repeated: plays=%d synth=%d", len(player.played), len(synth.texts))
	}
}
