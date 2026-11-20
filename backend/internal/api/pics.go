package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/np1-control-center/ncc/internal/pics"
	"github.com/np1-control-center/ncc/internal/platespeech"
)

const picsSettingsFile = "pics/settings.json"

type picsSettings struct {
	Enabled bool `json:"enabled"`
	Speech  bool `json:"speech"`
	Style   int  `json:"style"`
	Volume  int  `json:"volume"`
}

func defaultPICSSettings() picsSettings {
	return picsSettings{Enabled: true, Style: 1, Volume: 60}
}

func (v picsSettings) validate() error {
	if v.Style < 0 || v.Style > 2147483647 || v.Volume < 0 || v.Volume > 100 {
		return errors.New("VOICEVOXの声と音量を確認してください")
	}
	return nil
}

type picsService struct {
	path  string
	voice *platespeech.Manager
	scan  func(context.Context, func(), func(string, []byte)) error

	mu                                  sync.Mutex
	settings                            picsSettings
	monitor                             pics.Monitor
	scanState, scanReason, speechReason string
	scanChanged, speechChanged          chan struct{}
	scanCancel, prepCancel, playCancel  context.CancelFunc
	playing                             *pics.Cue
}

func newPICSService(dir string, voice *platespeech.Manager) *picsService {
	p := &picsService{path: filepath.Join(dir, picsSettingsFile), voice: voice, settings: defaultPICSSettings(), scanState: "starting", scanChanged: make(chan struct{}, 1), speechChanged: make(chan struct{}, 1)}
	if data, err := os.ReadFile(p.path); err == nil {
		settings := p.settings
		if json.Unmarshal(data, &settings) == nil && settings.validate() == nil {
			p.settings = settings
		}
	}
	return p
}

func wakePICS(ch chan struct{}) {
	select {
	case ch <- struct{}{}:
	default:
	}
}

func (p *picsService) save(settings picsSettings) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if settings == p.settings {
		return nil
	}
	data, _ := json.Marshal(settings)
	if err := writeStateFile(p.path, data); err != nil {
		return err
	}
	if settings.Enabled != p.settings.Enabled {
		if p.scanCancel != nil {
			p.scanCancel()
		}
		p.monitor = pics.Monitor{}
		p.scanState, p.scanReason = "starting", ""
		wakePICS(p.scanChanged)
	}
	if p.playCancel != nil {
		p.playCancel()
	}
	p.settings = settings
	p.restartPreparation()
	return nil
}

// restartPreparation is called with p.mu held.
func (p *picsService) restartPreparation() {
	if p.prepCancel != nil {
		p.prepCancel()
	}
	p.speechReason = ""
	wakePICS(p.speechChanged)
}

func picsWait(ctx context.Context, changed <-chan struct{}, retry time.Duration) {
	var next <-chan time.Time
	if retry > 0 {
		timer := time.NewTimer(retry)
		defer timer.Stop()
		next = timer.C
	}
	select {
	case <-ctx.Done():
	case <-changed:
	case <-next:
	}
}

func (p *picsService) run(ctx context.Context) {
	go p.runScan(ctx)
	go p.prepareSpeech(ctx)
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			p.tickSpeech(ctx)
		}
	}
}

func (p *picsService) runScan(ctx context.Context) {
	for ctx.Err() == nil {
		p.mu.Lock()
		if !p.settings.Enabled {
			p.mu.Unlock()
			picsWait(ctx, p.scanChanged, 0)
			continue
		}
		scanCtx, cancel := context.WithCancel(ctx)
		p.scanCancel = cancel
		p.mu.Unlock()
		err := p.scan(scanCtx, func() {
			p.mu.Lock()
			defer p.mu.Unlock()
			if scanCtx.Err() == nil {
				p.scanState, p.scanReason = "scanning", ""
			}
		}, func(source string, data []byte) {
			p.mu.Lock()
			defer p.mu.Unlock()
			if scanCtx.Err() == nil {
				p.monitor.Receive(source, data, time.Now())
				p.cancelInvalidCue(time.Now())
			}
		})
		p.mu.Lock()
		p.scanCancel = nil
		p.monitor = pics.Monitor{}
		p.cancelInvalidCue(time.Now())
		if scanCtx.Err() == nil && err != nil {
			p.scanState, p.scanReason = "unavailable", err.Error()
		}
		p.mu.Unlock()
		cancel()
		picsWait(ctx, p.scanChanged, 15*time.Second)
	}
}

func (p *picsService) prepareSpeech(ctx context.Context) {
	for ctx.Err() == nil {
		p.mu.Lock()
		cfg := p.settings
		if !cfg.Enabled || !cfg.Speech {
			p.mu.Unlock()
			picsWait(ctx, p.speechChanged, 0)
			continue
		}
		prep, cancel := context.WithCancel(ctx)
		p.prepCancel = cancel
		p.mu.Unlock()
		err := p.voice.Prepare(prep, cfg.Style, pics.Vocabulary())
		p.mu.Lock()
		p.prepCancel = nil
		if prep.Err() == nil && err != nil {
			p.speechReason = err.Error()
		} else if err == nil {
			p.speechReason = ""
		}
		p.mu.Unlock()
		cancel()
		var retry time.Duration
		if err != nil {
			retry = 2 * time.Minute
		}
		picsWait(ctx, p.speechChanged, retry)
	}
}

func (p *picsService) cancelInvalidCue(now time.Time) {
	if p.playing != nil && !p.monitor.StillValid(*p.playing, now) {
		p.playCancel()
	}
}

func (p *picsService) tickSpeech(ctx context.Context) {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := time.Now()
	p.cancelInvalidCue(now)
	cfg := p.settings
	if !cfg.Enabled || !cfg.Speech || cfg.Volume == 0 || p.playing != nil {
		return
	}
	cue, ok := p.monitor.NextCue(now)
	if !ok {
		return
	}
	if p.voice.HardwareAvailable() != nil {
		return
	}
	texts := pics.SpeechParts(cue)
	for _, text := range texts {
		if !p.voice.Prepared(cfg.Style, text) {
			return
		}
	}
	play, cancel := context.WithCancel(ctx)
	p.playing, p.playCancel = &cue, cancel
	go func() {
		defer cancel()
		_ = p.voice.PlayPrepared(play, cfg.Style, cfg.Volume, texts, func(length time.Duration) bool {
			p.mu.Lock()
			defer p.mu.Unlock()
			now := time.Now()
			if !p.monitor.Valid(cue, now) || play.Err() != nil || cue.Kind != "green" && length >= time.Duration(cue.Seconds)*time.Second {
				return false
			}
			p.monitor.Claim(cue, now)
			return true
		})
		p.mu.Lock()
		p.playing, p.playCancel = nil, nil
		p.mu.Unlock()
	}()
}

func (s *Server) registerPICS() {
	p := s.pics
	s.mux.HandleFunc("GET /api/v1/pics", func(w http.ResponseWriter, r *http.Request) {
		p.mu.Lock()
		out := struct {
			Settings picsSettings  `json:"settings"`
			State    string        `json:"state"`
			Reason   string        `json:"reason,omitempty"`
			Signals  []pics.Signal `json:"signals"`
		}{p.settings, p.scanState, p.scanReason, p.monitor.Signals(time.Now())}
		p.mu.Unlock()
		if !out.Settings.Enabled {
			out.State, out.Reason = "off", ""
		}
		jsonOut(w, http.StatusOK, out)
	})
	s.mux.HandleFunc("GET /api/v1/pics/speech", func(w http.ResponseWriter, r *http.Request) {
		p.mu.Lock()
		cfg, reason := p.settings, p.speechReason
		p.mu.Unlock()
		ready := p.voice.Prepared(cfg.Style, pics.PreviewText)
		if err := p.voice.HardwareAvailable(); err != nil {
			ready, reason = false, err.Error()
		}
		styles := p.voice.CachedStyles()
		credit := "VOICEVOX"
		if cfg.Style == 1 {
			credit = "VOICEVOX:ずんだもん"
		}
		for _, style := range styles {
			if style.ID == cfg.Style {
				credit = "VOICEVOX:" + style.Speaker
				break
			}
		}
		jsonOut(w, http.StatusOK, map[string]any{"ready": ready, "reason": reason, "styles": styles, "credit": credit})
	})
	s.mux.HandleFunc("PUT /api/v1/pics", func(w http.ResponseWriter, r *http.Request) {
		var body *picsSettings
		if !decode(w, r, &body) {
			return
		}
		if body == nil {
			fail(w, http.StatusBadRequest, "INVALID_PICS_SETTINGS", "高度化PICSの設定を指定してください")
			return
		}
		cfg := *body
		if err := cfg.validate(); err != nil {
			fail(w, http.StatusBadRequest, "INVALID_PICS_SETTINGS", err.Error())
			return
		}
		if err := p.save(cfg); err != nil {
			fail(w, http.StatusInternalServerError, "PICS_SAVE_FAILED", "高度化PICSの設定を保存できません")
			return
		}
		s.audit("pics.settings", "saved")
		jsonOut(w, http.StatusOK, cfg)
	})
	s.mux.HandleFunc("POST /api/v1/pics/speech/preview", func(w http.ResponseWriter, r *http.Request) {
		p.mu.Lock()
		cfg := p.settings
		p.mu.Unlock()
		if !cfg.Enabled || !cfg.Speech || cfg.Volume == 0 {
			fail(w, http.StatusConflict, "PICS_SPEECH_OFF", "発話と再生音量を有効にしてください")
			return
		}
		if err := p.voice.PlayPrepared(r.Context(), cfg.Style, cfg.Volume, []string{pics.PreviewText}, func(_ time.Duration) bool {
			p.mu.Lock()
			defer p.mu.Unlock()
			return cfg == p.settings
		}); err != nil {
			fail(w, http.StatusConflict, "PICS_SPEECH_PREVIEW_FAILED", err.Error())
			return
		}
		jsonOut(w, http.StatusOK, map[string]bool{"played": true})
	})
}
