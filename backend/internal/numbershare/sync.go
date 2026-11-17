// Package numbershare sends confirmed, locally stored plate-recognition events
// to a NumberShare server. It never sends images or rule metadata. Coordinates
// are included only when the root-managed configuration explicitly opts in.
package numbershare

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/np1-control-center/ncc/internal/config"
	"github.com/np1-control-center/ncc/internal/plate"
)

const (
	defaultBaseURL = "https://nmch.jp/app/numbershare/api/"
	defaultGrace   = 45 * time.Second
	defaultScan    = 30 * time.Second
	maxResponse    = 64 * 1024
	maxPerPass     = 20
)

var (
	serialPattern = regexp.MustCompile(`^[0-9]{1,4}$`)
	classPattern  = regexp.MustCompile(`^[0-9A-Z]{2,3}$`)
)

// Config is root-managed and must be mode 0640 or stricter. APIKey is never
// copied to status or state files.
type Config struct {
	Enabled           bool      `json:"enabled"`
	BaseURL           string    `json:"baseUrl"`
	APIKey            string    `json:"apiKey"`
	CutoverAt         time.Time `json:"cutoverAt"`
	SendLocation      bool      `json:"sendLocation,omitempty"`
	MergeGraceSeconds int       `json:"mergeGraceSeconds,omitempty"`
}

type itemState struct {
	EventID      string    `json:"eventId"`
	Sent         bool      `json:"sent,omitempty"`
	Permanent    bool      `json:"permanent,omitempty"`
	Attempts     int       `json:"attempts,omitempty"`
	NextAttempt  time.Time `json:"nextAttempt,omitempty"`
	LastHTTPCode int       `json:"lastHttpCode,omitempty"`
	Result       *Result   `json:"result,omitempty"`
}

type state struct {
	Version       int                  `json:"version"`
	Items         map[string]itemState `json:"items"`
	LastAttemptAt time.Time            `json:"lastAttemptAt,omitempty"`
	LastSuccessAt time.Time            `json:"lastSuccessAt,omitempty"`
	LastError     string               `json:"lastError,omitempty"`
}

type platePayload struct {
	SerialNumber         string `json:"serial_number"`
	Region               string `json:"region,omitempty"`
	ClassificationNumber string `json:"classification_number,omitempty"`
	Hiragana             string `json:"hiragana,omitempty"`
}

type locationPayload struct {
	Latitude  float64  `json:"latitude"`
	Longitude float64  `json:"longitude"`
	AccuracyM *float64 `json:"accuracy_m,omitempty"`
}

type sightingPayload struct {
	EventID    string           `json:"event_id"`
	ObservedAt string           `json:"observed_at"`
	Plate      platePayload     `json:"plate"`
	Location   *locationPayload `json:"location,omitempty"`
}

// Result is the user-visible outcome returned for one locally recorded plate.
// It deliberately excludes the remote database ID, account/device identity and
// every authentication field. Category and comment are populated only for an
// exact match; candidates must never be presented as confirmed metadata.
type Result struct {
	Status          string    `json:"status"`
	Category        string    `json:"category,omitempty"`
	Comment         string    `json:"comment,omitempty"`
	MetadataPresent bool      `json:"metadataPresent,omitempty"`
	CheckedAt       time.Time `json:"checkedAt"`
}

type serverPlate struct {
	Category        string `json:"category"`
	Comment         string `json:"comment"`
	MetadataPresent bool   `json:"metadata_present"`
}

type serverMatch struct {
	Status string       `json:"status"`
	Plate  *serverPlate `json:"plate"`
}

type serverResponse struct {
	Saved bool        `json:"saved"`
	Match serverMatch `json:"match"`
}

// CheckResult intentionally exposes no account, device, API-key, or response
// body data. It is suitable for local health checks and deployment gates.
type CheckResult struct {
	Reachable     bool   `json:"reachable"`
	Authenticated bool   `json:"authenticated"`
	Status        string `json:"status"`
}

type Syncer struct {
	ConfigPath string
	StateDir   string
	Clock      func() time.Time
	Client     *http.Client
}

func NewSyncer(configPath, stateDir string) *Syncer {
	return &Syncer{
		ConfigPath: configPath,
		StateDir:   stateDir,
		Clock:      time.Now,
		Client:     newHTTPClient(),
	}
}

// Check validates the configured device key from the NP1 itself without
// creating a sighting. Response bodies are deliberately discarded.
func (s *Syncer) Check(ctx context.Context) (CheckResult, error) {
	c, err := loadConfig(s.ConfigPath)
	if err != nil {
		return CheckResult{Status: "config"}, err
	}
	if !c.Enabled {
		return CheckResult{Status: "disabled"}, errors.New("disabled")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"users/me", nil)
	if err != nil {
		return CheckResult{Status: "request"}, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-API-Key", c.APIKey)
	resp, err := s.Client.Do(req)
	if err != nil {
		return CheckResult{Status: "transport"}, err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxResponse))
	result := CheckResult{Reachable: true, Authenticated: resp.StatusCode == http.StatusOK}
	if result.Authenticated {
		result.Status = "ok"
		return result, nil
	}
	result.Status = fmt.Sprintf("http-%d", resp.StatusCode)
	return result, errors.New(result.Status)
}

func loadConfig(path string) (Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	var c Config
	if err = json.Unmarshal(b, &c); err != nil {
		return Config{}, errors.New("invalid-config")
	}
	if c.BaseURL == "" {
		c.BaseURL = defaultBaseURL
	}
	u, err := url.Parse(c.BaseURL)
	loopbackHTTP := err == nil && u.Scheme == "http" && (u.Hostname() == "127.0.0.1" || u.Hostname() == "localhost" || u.Hostname() == "::1")
	if err != nil || (u.Scheme != "https" && !loopbackHTTP) || u.Host == "" || u.RawQuery != "" || u.Fragment != "" {
		return Config{}, errors.New("invalid-base-url")
	}
	if !strings.HasSuffix(u.Path, "/") {
		return Config{}, errors.New("base-url-must-end-in-slash")
	}
	if c.Enabled && (strings.TrimSpace(c.APIKey) == "" || c.CutoverAt.IsZero()) {
		return Config{}, errors.New("enabled-config-incomplete")
	}
	if c.MergeGraceSeconds != 0 && (c.MergeGraceSeconds < 30 || c.MergeGraceSeconds > 600) {
		return Config{}, errors.New("invalid-merge-grace")
	}
	return c, nil
}

func (s *Syncer) statePath() string {
	return filepath.Join(s.StateDir, "plate", "numbershare-sync.json")
}

func (s *Syncer) loadState() state {
	st := state{Version: 2, Items: map[string]itemState{}}
	b, err := os.ReadFile(s.statePath())
	if err == nil {
		_ = json.Unmarshal(b, &st)
	}
	if st.Items == nil {
		st.Items = map[string]itemState{}
	}
	st.Version = 2
	return st
}

func (s *Syncer) saveState(st state) error {
	return config.Atomic(s.statePath(), st)
}

func eventID(localID string) string {
	b := sha256.Sum256([]byte("np1-numbershare:" + localID))
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	h := hex.EncodeToString(b[:16])
	return h[:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
}

func serialDisplay(number string) string {
	if len(number) == 4 {
		return number[:2] + "-" + number[2:]
	}
	return number
}

func oneAllowedKana(value string) bool {
	r, n := utf8.DecodeRuneInString(value)
	if n != len(value) || r == utf8.RuneError {
		return false
	}
	return unicode.Is(unicode.Hiragana, r) || strings.ContainsRune("ABEKMTYH", r)
}

func payload(r plate.Record, sendLocation bool) (sightingPayload, bool) {
	number := r.Reading.Number
	if !serialPattern.MatchString(number) {
		return sightingPayload{}, false
	}
	p := platePayload{SerialNumber: serialDisplay(number)}
	if utf8.RuneCountInString(r.Reading.Region) >= 1 && utf8.RuneCountInString(r.Reading.Region) <= 16 {
		p.Region = r.Reading.Region
	}
	if classPattern.MatchString(r.Reading.Class) {
		p.ClassificationNumber = r.Reading.Class
	}
	if oneAllowedKana(r.Reading.Kana) {
		p.Hiragana = r.Reading.Kana
	}
	out := sightingPayload{EventID: eventID(r.ID), ObservedAt: r.FirstSeen.Format(time.RFC3339Nano), Plate: p}
	if sendLocation && r.Location != nil && r.Location.Lat >= -90 && r.Location.Lat <= 90 && r.Location.Lon >= -180 && r.Location.Lon <= 180 {
		out.Location = &locationPayload{Latitude: r.Location.Lat, Longitude: r.Location.Lon, AccuracyM: r.Location.AccuracyM}
	}
	return out, true
}

func syncRetryDelay(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	d := 15 * time.Second * time.Duration(1<<min(attempt-1, 5))
	if d > 10*time.Minute {
		return 10 * time.Minute
	}
	return d
}

func responseResult(out serverResponse, at time.Time) (*Result, error) {
	if !out.Saved {
		return nil, errors.New("server-did-not-confirm-save")
	}
	result := &Result{Status: out.Match.Status, CheckedAt: at.UTC()}
	switch out.Match.Status {
	case "exact":
		if out.Match.Plate == nil {
			return nil, errors.New("invalid-exact-match")
		}
		category := strings.TrimSpace(out.Match.Plate.Category)
		comment := strings.TrimSpace(out.Match.Plate.Comment)
		if !utf8.ValidString(category) || !utf8.ValidString(comment) || utf8.RuneCountInString(category) > 64 || utf8.RuneCountInString(comment) > 500 {
			return nil, errors.New("invalid-match-metadata")
		}
		result.Category = category
		result.Comment = comment
		result.MetadataPresent = out.Match.Plate.MetadataPresent
	case "candidate", "none":
		// The server intentionally omits category/comment for candidates so an
		// ambiguous recognition cannot become a warning about the wrong car.
	case "":
		return nil, errors.New("missing-match-status")
	default:
		return nil, errors.New("invalid-match-status")
	}
	return result, nil
}

func (s *Syncer) post(ctx context.Context, c Config, p sightingPayload) (int, *Result, error) {
	b, err := json.Marshal(p)
	if err != nil {
		return 0, nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"sightings", bytes.NewReader(b))
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-API-Key", c.APIKey)
	resp, err := s.Client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	data, readErr := io.ReadAll(io.LimitReader(resp.Body, maxResponse+1))
	if readErr != nil || len(data) > maxResponse {
		return resp.StatusCode, nil, errors.New("invalid-response")
	}
	if resp.StatusCode != http.StatusOK {
		return resp.StatusCode, nil, fmt.Errorf("http-%d", resp.StatusCode)
	}
	var out serverResponse
	if json.Unmarshal(data, &out) != nil {
		return resp.StatusCode, nil, errors.New("invalid-response")
	}
	result, err := responseResult(out, s.Clock())
	return resp.StatusCode, result, err
}

func statusClass(code int, err error) string {
	if err == nil {
		return ""
	}
	if code != 0 {
		return fmt.Sprintf("http-%d", code)
	}
	return "transport"
}

// SyncOnce sends only records created after CutoverAt, only after their merge
// window has settled. Existing records are never backfilled implicitly.
func (s *Syncer) SyncOnce(ctx context.Context) (int, error) {
	c, err := loadConfig(s.ConfigPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0, nil
		}
		return 0, err
	}
	if !c.Enabled {
		return 0, nil
	}
	now := s.Clock()
	grace := defaultGrace
	if c.MergeGraceSeconds > 0 {
		grace = time.Duration(c.MergeGraceSeconds) * time.Second
	}
	from := plate.Day(c.CutoverAt.AddDate(0, 0, -1))
	records, _, err := plate.NewStore(filepath.Join(s.StateDir, "plate")).List(plate.Query{From: from, To: plate.Day(now), Limit: plate.MaxListLimit})
	if err != nil {
		return 0, err
	}
	sort.Slice(records, func(i, j int) bool { return records[i].FirstSeen.Before(records[j].FirstSeen) })
	st := s.loadState()
	sent := 0
	for _, record := range records {
		if record.FirstSeen.Before(c.CutoverAt) || now.Sub(record.LastSeen) < grace {
			continue
		}
		item := st.Items[record.ID]
		if item.Sent || item.Permanent || (!item.NextAttempt.IsZero() && now.Before(item.NextAttempt)) {
			continue
		}
		p, ok := payload(record, c.SendLocation)
		if !ok {
			item.Permanent = true
			item.EventID = eventID(record.ID)
			st.Items[record.ID] = item
			continue
		}
		item.EventID = p.EventID
		st.LastAttemptAt = now.UTC()
		code, result, postErr := s.post(ctx, c, p)
		saved := postErr == nil && result != nil
		item.LastHTTPCode = code
		if saved {
			item.Sent = true
			item.Result = result
			item.NextAttempt = time.Time{}
			st.LastSuccessAt = now.UTC()
			st.LastError = ""
			sent++
		} else {
			item.Attempts++
			st.LastError = statusClass(code, postErr)
			switch code {
			case http.StatusUnprocessableEntity, http.StatusConflict, http.StatusGone:
				item.Permanent = true
			case http.StatusUnauthorized, http.StatusForbidden:
				item.NextAttempt = now.Add(time.Hour)
			default:
				item.NextAttempt = now.Add(syncRetryDelay(item.Attempts))
			}
		}
		st.Items[record.ID] = item
		if err = s.saveState(st); err != nil {
			return sent, err
		}
		if !saved || sent >= maxPerPass {
			break
		}
	}
	return sent, s.saveState(st)
}

// Results loads only the user-visible NumberShare outcomes keyed by local
// plate-record ID. Operational retry state and remote credentials are never
// returned to the dashboard API.
func Results(stateDir string) map[string]Result {
	s := NewSyncer("", stateDir)
	st := s.loadState()
	out := make(map[string]Result)
	for localID, item := range st.Items {
		if item.Sent && item.Result != nil {
			out[localID] = *item.Result
		}
	}
	return out
}

func (s *Syncer) Run(ctx context.Context) error {
	ticker := time.NewTicker(defaultScan)
	defer ticker.Stop()
	for {
		_, _ = s.SyncOnce(ctx)
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}
