package pics

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/hex"
	"math"
	"os"
	"os/exec"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/np1-control-center/ncc/internal/platespeech"
)

const trialVoiceCache = "/overlay/owner-apps/np1-control/state/plate/speech"

type trialPlayer struct {
	platespeech.Pacat
	pcm    []byte
	silent bool
}

func (p *trialPlayer) Play(ctx context.Context, pcm []byte, volume int) error {
	p.pcm = pcm
	if p.silent {
		return nil
	}
	return p.Pacat.Play(ctx, pcm, volume)
}

// Only present in the standalone test executable. Broadcast data from this
// trial can never trigger the installed application or a PICS receiver.
func TestPICSDevice(t *testing.T) {
	mode := os.Getenv("NCC_PICS_DEVICE_MODE")
	if mode == "" {
		t.Skip("requires a connected NP1 and the explicitly launched hardware trial")
	}
	if mode == "prepare" {
		if os.Geteuid() <= 0 {
			t.Fatal("prepare the cache as the NP1 web service user")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		voice := platespeech.NewManager(trialVoiceCache, platespeech.NewClient(), nil)
		if err := voice.Prepare(ctx, 1, Vocabulary()); err != nil {
			t.Fatal(err)
		}
		t.Logf("NP1 VOICEVOX cache ready: %d clips", len(Vocabulary()))
		return
	}
	if mode != "radio" && mode != "replay" && mode != "observe" {
		t.Fatal("use the radio, observe, replay or prepare trial")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 65*time.Second)
	defer cancel()
	player := &trialPlayer{
		Pacat:  platespeech.Pacat{Path: "/usr/bin/pacat"},
		silent: os.Getenv("NCC_PICS_DEVICE_SILENT") == "1",
	}
	voice := platespeech.NewManager(trialVoiceCache, nil, player)
	if player.silent {
		for _, text := range Vocabulary() {
			if !voice.Prepared(1, text) {
				t.Fatalf("a prepared VOICEVOX clip is missing: %q", text)
			}
		}
		t.Logf("All %d cached VOICEVOX clips are present", len(Vocabulary()))
	}
	var monitor Monitor
	var cue Cue
	if mode == "replay" {
		now := time.Now()
		monitor.Receive("trial", frame(1, 0x81), now)
		monitor.Receive("trial", frame(1, 0x83), now.Add(time.Millisecond))
		var found bool
		cue, found = monitor.NextCue(now.Add(time.Millisecond))
		if !found || cue.Kind != "green" {
			t.Fatal("local replay did not propose the green cue")
		}
	} else {
		tag, err := hex.DecodeString(os.Getenv("NCC_PICS_DEVICE_TAG"))
		if err != nil || len(tag) != 8 {
			t.Fatal("an eight-byte trial tag is required")
		}
		decode := func(data []byte) (string, []byte, bool) {
			address, payload, done := stockAdvertisementFor(data, 0xffff)
			if !bytes.HasPrefix(payload, tag) {
				return address, nil, done
			}
			return address, payload[len(tag):], done
		}
		scan, stop := context.WithTimeout(ctx, 38*time.Second)
		red, green := 0, 0
		var last time.Time
		receive := func(source string, payload []byte) {
			now := time.Now()
			monitor.Receive(source, payload, now)
			if n := len(payload); n > 10 {
				color := payload[10] & 15
				if !last.IsZero() {
					t.Logf("trial color=%d interval=%s", color, now.Sub(last).Round(time.Millisecond))
				}
				last = now
				switch color {
				case 1:
					red++
				case 3:
					green++
				}
			}
			if next, ok := monitor.NextCue(now); ok && next.Kind == "green" {
				cue = next
				stop()
			}
		}
		if mode == "observe" {
			caller, parseErr := strconv.ParseUint(os.Getenv("NCC_PICS_BROKER_PID"), 10, 32)
			if parseErr != nil || caller < 2 {
				t.Fatal("the installed broker PID is required to correlate notifications")
			}
			err = observeTrialDiscovery(scan, uint32(caller), decode, receive)
		} else {
			err = scanStockBLEWith(scan, new(time.Time), decode, func() { t.Log("stock BLE reports received") }, receive)
		}
		stop()
		t.Logf("matched over-air trial reports red=%d green=%d", red, green)
		if cue.Kind == "" {
			t.Fatalf("stock BLE did not observe a continuous red-to-green change: %v", err)
		}
	}
	testTrialPlayback(t, ctx, voice, player, SpeechParts(cue), func(time.Duration) bool {
		return monitor.Valid(cue, time.Now())
	})
	if mode == "replay" {
		for _, other := range []byte{0x82, 0x81} {
			var crossing Monitor
			now := time.Now()
			crossing.Receive("trial", frame(1, 0x81, 0x83), now)
			crossing.Receive("trial", frame(1, 0x81, other), now.Add(time.Millisecond))
			hint, ok := crossing.NextCue(now.Add(time.Millisecond))
			if !ok || hint.Kind != otherBlinkCue && hint.Kind != soonCue {
				t.Fatal("local replay did not propose the other-direction cue")
			}
			testTrialPlayback(t, ctx, voice, player, SpeechParts(hint), func(time.Duration) bool { return crossing.Valid(hint, time.Now()) })
		}
		cues := []Cue{{Kind: "remaining", Seconds: 27}, {Kind: "remaining", Seconds: 10}}
		for n := 5; n >= 1; n-- {
			cues = append(cues, Cue{Kind: "number", Seconds: n})
		}
		for _, cue := range cues {
			testTrialPlayback(t, ctx, voice, player, SpeechParts(cue), func(time.Duration) bool { return true })
		}
	}
}

// Observes the installed broker's existing scan; this test sends no request to the controller.
func observeTrialDiscovery(ctx context.Context, caller uint32, decode advertisementDecoder, receive func(string, []byte)) error {
	session, err := connectStockBLE(ctx)
	if err != nil {
		return err
	}
	defer session.conn.Close()
	signals := make(chan *dbus.Signal, 64)
	session.conn.Signal(signals)
	defer session.conn.RemoveSignal(signals)
	if err := session.watchDiscovery(ctx); err != nil {
		return err
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case signal, ok := <-signals:
			if !ok {
				return context.Canceled
			}
			if data, ok := session.discoveryData(ctx, signal, caller); ok {
				if source, payload, _ := decode(data); source != "" && payload != nil {
					receive(source, payload)
				}
			}
		}
	}
}

func testTrialPlayback(t *testing.T, ctx context.Context, voice *platespeech.Manager, player *trialPlayer, phrase []string, allow func(time.Duration) bool) {
	t.Helper()
	if player.silent {
		player.pcm = nil
		if err := voice.PlayPrepared(ctx, 1, 50, phrase, allow); err != nil {
			t.Fatal(err)
		}
		var peak float64
		for _, level := range trialEnvelope(player.pcm) {
			peak = math.Max(peak, level)
		}
		if len(player.pcm)%2 != 0 || len(player.pcm) < 4800 || peak == 0 {
			t.Fatal("cached phrase did not yield non-silent, 16-bit PCM")
		}
		t.Logf("cached VOICEVOX phrase %q produced %d sample bytes with no speaker playback", phrase, len(player.pcm))
		return
	}
	var recording bytes.Buffer
	recordCtx, stopRecord := context.WithCancel(ctx)
	defer stopRecord()
	cmd := exec.CommandContext(recordCtx, "/usr/bin/parec", "--server=unix:/run/pulse/native", "--raw", "--device=primary.monitor", "--format=s16le", "--rate=24000", "--channels=1")
	cmd.Stdout = &recording
	if err := cmd.Start(); err != nil {
		t.Fatal("NP1 speaker monitor could not start")
	}
	defer func() { stopRecord(); _ = cmd.Wait() }()
	time.Sleep(250 * time.Millisecond)
	if err := voice.PlayPrepared(ctx, 1, 50, phrase, allow); err != nil {
		t.Fatal(err)
	}
	time.Sleep(250 * time.Millisecond)
	_ = cmd.Process.Signal(syscall.SIGTERM)
	_ = cmd.Wait()
	match := trialAudioMatch(player.pcm, recording.Bytes())
	t.Logf("cached VOICEVOX phrase %q on NP1 primary.monitor match=%.3f", phrase, match)
	if match < 0.85 {
		t.Fatal("NP1 speaker sink did not reproduce the expected VOICEVOX phrase")
	}
}

func trialEnvelope(raw []byte) []float64 {
	const samples = 240 // 10 ms at 24 kHz
	out := make([]float64, len(raw)/(2*samples))
	for i := range out {
		for j := 0; j < samples; j++ {
			pos := 2 * (i*samples + j)
			x := float64(int16(binary.LittleEndian.Uint16(raw[pos:])))
			out[i] += x * x
		}
		out[i] = math.Sqrt(out[i] / samples)
	}
	return out
}

func trialAudioMatch(expected, recorded []byte) float64 {
	reference, capture := trialEnvelope(expected), trialEnvelope(recorded)
	var energy, maximum float64
	for _, x := range reference {
		energy += x * x
	}
	if energy == 0 || len(reference) < 10 {
		return 0
	}
	for i := 0; i+len(reference) <= len(capture); i++ {
		var dot, level float64
		for j, x := range reference {
			y := capture[i+j]
			dot += x * y
			level += y * y
		}
		if level > energy*0.001 {
			maximum = math.Max(maximum, dot/math.Sqrt(energy*level))
		}
	}
	return maximum
}
