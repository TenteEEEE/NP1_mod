package update

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/np1-control-center/ncc/internal/otapolicy"
)

const (
	baseDir       = "/overlay/owner-apps/np1-control"
	modeOff       = "off"
	modeCheck     = "check"
	modeBoot      = "next-boot"
	modeNow       = "immediate"
	checkInterval = 6 * time.Hour
	retryInterval = 15 * time.Minute
)

type settings struct {
	Mode    string `json:"mode"`
	Allow4G bool   `json:"allow4G"`
}

type queued struct {
	Boot      string `json:"boot"`
	Version   string `json:"version"`
	Raw       []byte `json:"manifest"`
	Signature []byte `json:"signature"`
}

type state struct {
	Settings    settings   `json:"settings"`
	Current     string     `json:"current"`
	Highest     string     `json:"highest,omitempty"`
	LastChecked *time.Time `json:"lastChecked,omitempty"`
	NextCheck   time.Time  `json:"nextCheck"`
	Result      string     `json:"result,omitempty"`
	Message     string     `json:"message,omitempty"`
	Latest      *candidate `json:"latest,omitempty"`
	Pending     *queued    `json:"pending,omitempty"`
	FailedSHA   string     `json:"failedSha,omitempty"`
}

type status struct {
	Settings       settings   `json:"settings"`
	Current        string     `json:"current"`
	Latest         *candidate `json:"latest,omitempty"`
	PendingVersion string     `json:"pendingVersion,omitempty"`
	LastChecked    *time.Time `json:"lastChecked,omitempty"`
	Phase          string     `json:"phase,omitempty"`
	Result         string     `json:"result,omitempty"`
	Message        string     `json:"message,omitempty"`
}

type daemon struct {
	mu    sync.Mutex
	state state
	phase string
	boot  string
	repo  string
	key   ed25519.PublicKey
	dev   device
	fetch downloadFunc
}

func validMode(mode string) bool {
	switch mode {
	case modeOff, modeCheck, modeBoot, modeNow:
		return true
	}
	return false
}

func (d *daemon) statusLocked() status {
	s := d.state
	view := status{Settings: s.Settings, Current: s.Current, Latest: s.Latest, LastChecked: s.LastChecked, Phase: d.phase, Result: s.Result, Message: s.Message}
	if s.Pending != nil {
		view.PendingVersion = s.Pending.Version
	}
	return view
}

func (d *daemon) save(next state) error {
	if err := durableJSON(d.dev.file("state.json"), next); err != nil {
		return err
	}
	d.state = next
	return nil
}

func updateReply(w http.ResponseWriter, code int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(body)
}

func updateError(w http.ResponseWriter, code int, reason string) {
	updateReply(w, code, map[string]string{"reason": reason})
}

func readRequest(w http.ResponseWriter, r *http.Request, target any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 512))
	dec.DisallowUnknownFields()
	return dec.Decode(target) == nil && dec.Decode(new(any)) == io.EOF
}

func (d *daemon) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /update", func(w http.ResponseWriter, _ *http.Request) {
		d.mu.Lock()
		view := d.statusLocked()
		d.mu.Unlock()
		updateReply(w, http.StatusOK, view)
	})
	mux.HandleFunc("PUT /update", func(w http.ResponseWriter, r *http.Request) {
		var opts struct {
			Mode    string `json:"mode"`
			Allow4G *bool  `json:"allow4G"`
		}
		if !readRequest(w, r, &opts) || !validMode(opts.Mode) || opts.Allow4G == nil {
			updateError(w, http.StatusBadRequest, "更新モードと利用回線を指定してください")
			return
		}
		d.mu.Lock()
		defer d.mu.Unlock()
		if d.phase != "" {
			updateError(w, http.StatusConflict, "更新処理が終了してから設定を変更してください")
			return
		}
		next := d.state
		next.Settings = settings{Mode: opts.Mode, Allow4G: *opts.Allow4G}
		if next.Settings != d.state.Settings {
			next.NextCheck = time.Time{}
			if opts.Mode != modeBoot && next.Pending != nil {
				next.Pending = nil
				next.Result, next.Message = "cancelled", "次回起動時の更新予約を取り消しました"
			}
			if err := d.save(next); err != nil {
				updateError(w, http.StatusInternalServerError, "更新設定を保存できません")
				return
			}
			if next.Pending == nil {
				_ = os.Remove(d.dev.file("candidate.ncc"))
			}
		}
		updateReply(w, http.StatusOK, d.statusLocked())
	})
	for _, action := range []string{"check", "apply"} {
		mux.HandleFunc("POST /update/"+action, func(w http.ResponseWriter, r *http.Request) {
			if !readRequest(w, r, &struct{}{}) {
				updateError(w, http.StatusBadRequest, "更新操作の入力が不正です")
				return
			}
			d.mu.Lock()
			defer d.mu.Unlock()
			if d.phase != "" {
				updateError(w, http.StatusConflict, "更新処理を実行中です")
				return
			}
			d.startLocked(action)
			updateReply(w, http.StatusAccepted, d.statusLocked())
		})
	}
	return mux
}

func (d *daemon) startLocked(action string) {
	d.phase = "checking"
	if action == "boot" || action == "apply" && d.state.Pending != nil {
		d.phase = "applying"
	}
	go d.run(action, d.state)
}

func (d *daemon) tick() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if action := scheduledAction(d.state, d.phase, d.boot, time.Now()); action != "" {
		d.startLocked(action)
	}
}

func scheduledAction(s state, phase, boot string, now time.Time) string {
	if phase != "" || s.Settings.Mode == modeOff || now.Before(s.NextCheck) {
		return ""
	}
	if s.Pending == nil {
		return "auto"
	}
	if s.Settings.Mode == modeBoot && s.Pending.Boot != boot {
		return "boot"
	}
	return ""
}

func (d *daemon) setPhase(phase string) {
	d.mu.Lock()
	d.phase = phase
	d.mu.Unlock()
}

func (d *daemon) run(action string, next state) {
	ctx, cancel := context.WithTimeout(context.Background(), 9*time.Minute)
	defer cancel()
	err := d.runJob(ctx, action, &next)
	if err != nil {
		next.Result, next.Message = "error", err.Error()
		if errors.Is(err, errNoWiFi) {
			next.Result = "waiting-wifi"
		}
		if action != "check" || next.Settings.Mode == modeCheck {
			next.NextCheck = time.Now().Add(retryInterval)
		}
		log.Printf("NP1CC update: %v", err)
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.save(next); err != nil {
		log.Printf("saving update state: %v", err)
		next.Result, next.Message = "error", "更新状態を保存できません"
		d.state = next
	}
	d.phase = ""
}

func (d *daemon) runJob(ctx context.Context, action string, next *state) error {
	current, err := d.dev.installedVersion(ctx)
	if err != nil {
		return err
	}
	next.Current = current
	if next.Highest == "" || versionCompare(current, next.Highest) > 0 {
		next.Highest = current
	}
	if action == "boot" || action == "apply" && next.Pending != nil {
		return d.apply(ctx, next, *next.Pending)
	}
	wifiOnly := !next.Settings.Allow4G
	list, err := releases(ctx, d.fetch, d.repo, wifiOnly, next.Current)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	next.LastChecked, next.Latest = &now, nil
	if action == "auto" || next.Settings.Mode == modeCheck {
		next.NextCheck = now.Add(checkInterval)
	}
	next.Result, next.Message = "current", "利用できる新しいバージョンはありません"
	if len(list) == 0 {
		return nil
	}
	for _, release := range list {
		item, err := releaseCandidate(ctx, d.fetch, d.key, d.repo, wifiOnly, release)
		if err != nil {
			return err
		}
		if item.Reason == "" {
			if err := d.compatible(item.meta, next); err != nil {
				item.Reason = err.Error()
			}
		}
		if next.Latest == nil || item.Reason == "" {
			next.Latest = &item
		}
		if item.Reason != "" {
			continue
		}
		next.Result, next.Message = "available", "適用できる更新があります"
		if action != "apply" && (action != "auto" || next.Settings.Mode == modeCheck) {
			return nil
		}
		d.setPhase("downloading")
		if err := d.dev.stage(ctx, d.fetch, wifiOnly, item); err != nil {
			return err
		}
		pending := queued{Boot: d.boot, Version: item.Version, Raw: item.raw, Signature: item.sig}
		if action == "auto" && next.Settings.Mode == modeBoot {
			next.Pending = &pending
			next.NextCheck = time.Time{}
			next.Result, next.Message = "queued", "次回の本体起動時に適用します"
			return nil
		}
		return d.apply(ctx, next, pending)
	}
	next.Result, next.Message = "manual", next.Latest.Reason
	return nil
}

func (d *daemon) compatible(m manifest, s *state) error {
	if versionCompare(m.Version, s.Current) <= 0 || s.Highest != "" && versionCompare(m.Version, s.Highest) < 0 {
		return errors.New("現在または過去に適用した版より古い更新です")
	}
	if s.FailedSHA == m.BinarySHA {
		return errors.New("この更新は前回失敗しました。手動配備で確認してください")
	}
	current, err := fileHash(d.dev.binary(), maxBinary)
	if err != nil || !slices.Contains(m.From, current) || !otapolicy.FirmwareMatches() {
		return errors.New("この導入状態には手動配備が必要です")
	}
	for _, file := range []struct{ path, hash string }{
		{"/usr/local/bin/DriveRec", m.DriveRec},
		{filepath.Join(d.dev.base, "recorder-hook/libncc-driverec-hook.so"), m.Hook},
		{filepath.Join(d.dev.base, "experimental/ncc-recorder-injector"), m.Injector},
	} {
		if hash, err := fileHash(file.path, 128<<20); err != nil || hash != file.hash {
			return errors.New("録画連携の構成が更新パッケージに対応していません")
		}
	}
	return nil
}

func (d *daemon) apply(ctx context.Context, next *state, pending queued) error {
	d.setPhase("applying")
	m, err := verifyManifest(pending.Raw, pending.Signature, d.key)
	if err == nil {
		err = d.compatible(m, next)
	}
	if err != nil {
		next.Pending = nil
		_ = os.Remove(d.dev.file("candidate.ncc"))
		return err
	}
	err = d.dev.apply(ctx, m)
	if errors.Is(err, errUpdateBusy) || errors.Is(err, errUpdateNotReady) {
		if next.Pending == nil {
			_ = os.Remove(d.dev.file("candidate.ncc"))
		}
		return err
	}
	next.Pending = nil
	_ = os.Remove(d.dev.file("candidate.ncc"))
	if err != nil {
		next.FailedSHA = m.BinarySHA
		return err
	}
	next.Current, next.Highest = m.Version, m.Version
	next.Latest, next.FailedSHA = nil, ""
	next.Result, next.Message = "updated", "更新を適用しました"
	next.NextCheck = time.Now().Add(checkInterval)
	return nil
}

func Serve(ctx context.Context) error {
	dev := device{base: baseDir}
	key, err := loadPublicKey(dev.file("public.key"))
	if err != nil {
		return err
	}
	repo, err := os.ReadFile(dev.file("repository"))
	if err != nil || !repoName.MatchString(strings.TrimSpace(string(repo))) {
		return errors.New("invalid update repository")
	}
	boot, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil || strings.TrimSpace(string(boot)) == "" {
		return errors.New("boot ID unavailable")
	}
	d := daemon{boot: strings.TrimSpace(string(boot)), repo: strings.TrimSpace(string(repo)), key: key, dev: dev, fetch: fetchChild}
	d.state.Settings.Mode = modeCheck
	if err := dev.recover(ctx, true); err != nil {
		return err
	}
	if err := readJSON(dev.file("state.json"), &d.state); err != nil && !os.IsNotExist(err) {
		return err
	}
	if !validMode(d.state.Settings.Mode) {
		return errors.New("invalid saved update mode")
	}
	d.state.Current, err = dev.installedVersion(ctx)
	if err != nil {
		return err
	}
	if d.state.Highest == "" || versionCompare(d.state.Current, d.state.Highest) > 0 {
		d.state.Highest = d.state.Current
	}
	listener, err := dev.listen()
	if err != nil {
		return err
	}
	defer listener.Close()
	server := &http.Server{Handler: d.handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 8 * time.Second, WriteTimeout: 8 * time.Second, MaxHeaderBytes: 4096}
	go func() {
		timer := time.NewTimer(20 * time.Second)
		defer timer.Stop()
		for {
			select {
			case <-ctx.Done():
				_ = server.Close()
				return
			case <-timer.C:
				d.tick()
				timer.Reset(time.Minute)
			}
		}
	}()
	err = server.Serve(listener)
	if errors.Is(err, http.ErrServerClosed) || errors.Is(err, net.ErrClosed) {
		return nil
	}
	return err
}
