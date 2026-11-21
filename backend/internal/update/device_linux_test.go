package update

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newTestDevice(t *testing.T) device {
	t.Helper()
	d := device{base: t.TempDir()}
	for _, name := range []string{"updates", "recovery", "bin"} {
		if err := os.Mkdir(filepath.Join(d.base, name), 0700); err != nil {
			t.Fatal(err)
		}
	}
	return d
}

func TestModesScheduleAndWaitForRealReboot(t *testing.T) {
	now := time.Now()
	for _, tt := range []struct {
		mode, phase, pendingBoot string
		next                     time.Time
		want                     string
	}{
		{mode: modeOff},
		{mode: modeNow, want: "auto"},
		{mode: modeBoot, want: "auto"},
		{mode: modeCheck, want: "auto"},
		{mode: modeNow, phase: "applying"},
		{mode: modeNow, next: now.Add(checkInterval)},
		{mode: modeBoot, pendingBoot: "current"},
		{mode: modeBoot, pendingBoot: "previous", want: "boot"},
		{mode: modeOff, pendingBoot: "previous"},
	} {
		s := state{Settings: settings{Mode: tt.mode}, NextCheck: tt.next}
		if tt.pendingBoot != "" {
			s.Pending = &queued{Boot: tt.pendingBoot}
		}
		if got := scheduledAction(s, tt.phase, "current", now); got != tt.want {
			t.Errorf("%+v: got %q", tt, got)
		}
	}
}

func TestSettingsPersistAndCancelQueuedImage(t *testing.T) {
	d := daemon{dev: newTestDevice(t), state: state{Settings: settings{Mode: modeBoot}, Pending: &queued{Version: "0.5.1"}}}
	if err := os.WriteFile(d.dev.file("candidate.ncc"), []byte("example"), 0600); err != nil {
		t.Fatal(err)
	}
	post := func(body string) int {
		result := httptest.NewRecorder()
		d.handler().ServeHTTP(result, httptest.NewRequest(http.MethodPut, "/update", strings.NewReader(body)))
		return result.Code
	}
	if got := post(`{"mode":"off","allow4G":false,"url":"https://example.com"}`); got != http.StatusBadRequest {
		t.Fatalf("unexpected request accepted: %d", got)
	}
	if got := post(`{"mode":"off","allow4G":true}`); got != http.StatusOK {
		t.Fatalf("valid settings rejected: %d", got)
	}
	var saved state
	if err := readJSON(d.dev.file("state.json"), &saved); err != nil || saved.Settings != (settings{Mode: modeOff, Allow4G: true}) || saved.Pending != nil {
		t.Fatalf("settings=%+v, err=%v", saved, err)
	}
	if _, err := os.Stat(d.dev.file("candidate.ncc")); !os.IsNotExist(err) {
		t.Fatalf("queued image still present: %v", err)
	}
}

func TestStagingAndRecoveryUseVerifiedFiles(t *testing.T) {
	device := newTestDevice(t)
	var zipped bytes.Buffer
	writer := gzip.NewWriter(&zipped)
	image := []byte("replacement bytes")
	if _, err := writer.Write(image); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	m := testManifest()
	m.AssetSize, m.BinarySize = int64(zipped.Len()), int64(len(image))
	m.AssetSHA, m.BinarySHA = fmt.Sprintf("%x", sha256.Sum256(zipped.Bytes())), fmt.Sprintf("%x", sha256.Sum256(image))
	fetch := func(_ context.Context, wifiOnly bool, _ string, _ int64, out io.Writer) error {
		if !wifiOnly {
			t.Fatal("download lost selected network policy")
		}
		_, err := out.Write(zipped.Bytes())
		return err
	}
	if err := device.stage(context.Background(), fetch, true, candidate{meta: m}); err != nil {
		t.Fatal(err)
	}
	wrong := m
	wrong.BinarySHA = strings.Repeat("b", 64)
	if err := device.stage(context.Background(), fetch, true, candidate{meta: wrong}); err == nil {
		t.Fatal("accepted image with a mismatching hash")
	}
	if got, _ := os.ReadFile(device.file("candidate.ncc")); !bytes.Equal(got, image) {
		t.Fatal("failed staging replaced the known-good file")
	}
	prev := []byte("previous bytes")
	if err := os.WriteFile(device.file("previous.ncc"), prev, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(device.binary(), image, 0755); err != nil {
		t.Fatal(err)
	}
	txn := transaction{PreviousSHA: fmt.Sprintf("%x", sha256.Sum256(prev)), CandidateSHA: m.BinarySHA}
	if err := durableJSON(device.file("transaction.json"), txn); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := device.recover(context.Background(), false); err != nil {
			t.Fatal(err)
		}
	}
	if got, _ := os.ReadFile(device.binary()); !bytes.Equal(got, prev) {
		t.Fatal("did not restore the previous binary")
	}
	var s state
	if err := readJSON(device.file("state.json"), &s); err != nil || s.FailedSHA != m.BinarySHA || s.Result != "recovered" {
		t.Fatalf("lost interrupted transaction history: %+v %v", s, err)
	}
}
