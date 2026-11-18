package adapters

import (
	"bytes"
	"testing"

	"github.com/np1-control-center/ncc/internal/h264tail"
)

func TestCameraAVCCToAnnexBPreservesCompleteNALUnits(t *testing.T) {
	avcc := []byte{0, 0, 0, 3, 0x65, 1, 2, 0, 0, 0, 2, 0x41, 3}
	want := []byte{0, 0, 0, 1, 0x65, 1, 2, 0, 0, 0, 1, 0x41, 3}
	got, err := cameraAVCCToAnnexB(avcc)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("Annex B conversion = %x, %v", got, err)
	}
	for _, invalid := range [][]byte{{0, 0, 0}, {0, 0, 0, 0}, {0, 0, 0, 4, 0x65}} {
		if _, err := cameraAVCCToAnnexB(invalid); err == nil {
			t.Fatalf("invalid AVCC accepted: %x", invalid)
		}
	}
}

func TestCameraEncoderHubReturnsCurrentGOPAndDetectsLag(t *testing.T) {
	hub := &cameraEncoderHub{notify: make(chan struct{}), config: h264tail.Config{AVC: []byte{1}}}
	hub.frames = []cameraEncoderHubFrame{{id: 10, sample: h264tail.Sample{Keyframe: true}}, {id: 11}}
	_, frames, _, gap := hub.after(0)
	if gap || len(frames) != 2 || !frames[0].sample.Keyframe {
		t.Fatal("new viewer did not receive cached GOP", gap, len(frames))
	}
	_, frames, _, gap = hub.after(10)
	if gap || len(frames) != 1 || frames[0].id != 11 {
		t.Fatal("current subscriber did not advance exactly", gap, frames)
	}
	_, _, _, gap = hub.after(8)
	if !gap {
		t.Fatal("subscriber that missed a GOP was allowed to consume dependent frames")
	}
}
