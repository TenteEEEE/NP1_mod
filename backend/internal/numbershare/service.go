// Package numbershare sends locally recognized licence plates to a configured
// NumberShare API and keeps an on-device retry queue. API credentials never
// leave the backend process.
package numbershare

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/np1-control-center/ncc/internal/config"
	"github.com/np1-control-center/ncc/internal/plate"
)

const (
	maxResponseBytes = 2 << 20
	apiTimeLayout    = "2006-01-02T15:04:05.000Z07:00"
)

type APIConfig struct {
	Revision int    `json:"revision"`
	Enabled  bool   `json:"enabled"`
	Endpoint string `json:"endpoint"`
	APIKey   string `json:"apiKey"`
}

type PublicConfig struct {
	Revision      int    `json:"revision"`
	Enabled       bool   `json:"enabled"`
	Endpoint      string `json:"endpoint"`
	KeyConfigured bool   `json:"keyConfigured"`
	APIKey        string `json:"apiKey"`
}

type Plate struct {
	SerialNumber         string `json:"serial_number"`
	Region               string `json:"region,omitempty"`
	ClassificationNumber string `json:"classification_number,omitempty"`
	Hiragana             string `json:"hiragana,omitempty"`
}

type Location struct {
	Latitude   float64  `json:"latitude"`
	Longitude  float64  `json:"longitude"`
	AccuracyM  *float64 `json:"accuracy_m,omitempty"`
	ObservedAt string   `json:"observed_at,omitempty"`
}

type SightingPayload struct {
	EventID    string    `json:"event_id"`
	ObservedAt string    `json:"observed_at"`
	Plate      Plate     `json:"plate"`
	Location   *Location `json:"location,omitempty"`
}

type QueueItem struct {
	EventID     string          `json:"eventId"`
	RecordID    string          `json:"recordId"`
	Payload     SightingPayload `json:"payload"`
	State       string          `json:"state"` // pending | sent | blocked
	Attempts    int             `json:"attempts"`
	QueuedAt    time.Time       `json:"queuedAt"`
	LastAttempt *time.Time      `json:"lastAttemptAt,omitempty"`
	NextAttempt *time.Time      `json:"nextAttemptAt,omitempty"`
	SentAt      *time.Time      `json:"sentAt,omitempty"`
	HTTPStatus  int             `json:"httpStatus,omitempty"`
	LastError   string          `json:"lastError,omitempty"`
	MatchStatus string          `json:"matchStatus,omitempty"`
	Manual      bool            `json:"manual,omitempty"`
}

type UploadResult struct {
	Total    int `json:"total"`
	Queued   int `json:"queued"`
	Requeued int `json:"requeued"`
	Existing int `json:"existing"`
	Skipped  int `json:"skipped"`
}

type Status struct {
	Config  PublicConfig `json:"config"`
	Pending int          `json:"pending"`
	Sent    int          `json:"sent"`
	Blocked int          `json:"blocked"`
	Items   []QueueItem  `json:"items"`
}

type Service struct {
	dir    string
	client *http.Client
	clock  func() time.Time
	wake   chan struct{}

	mu    sync.Mutex
	cfg   APIConfig
	queue []QueueItem
}

func New(stateDir string, client *http.Client) *Service {
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	s := &Service{dir: filepath.Join(stateDir, "numbershare"), client: client, clock: time.Now, wake: make(chan struct{}, 1), cfg: APIConfig{Revision: 1}, queue: []QueueItem{}}
	if b, err := os.ReadFile(s.configPath()); err == nil {
		var saved APIConfig
		if json.Unmarshal(b, &saved) == nil && saved.Revision > 0 {
			s.cfg = saved
		}
	}
	if b, err := os.ReadFile(s.queuePath()); err == nil {
		var saved []QueueItem
		if json.Unmarshal(b, &saved) == nil && saved != nil {
			s.queue = saved
			// Older clients emitted RFC3339Nano with up to nine fractional digits,
			// while NumberShare accepts the documented millisecond representation.
			// Keep rejected items blocked. Only an explicit period upload may return
			// records in the owner's selected date range to the retry queue.
			changed := false
			for i := range s.queue {
				item := &s.queue[i]
				parsed, parseErr := time.Parse(time.RFC3339Nano, item.Payload.ObservedAt)
				if parseErr != nil {
					continue
				}
				normalized := parsed.Format(apiTimeLayout)
				if normalized == item.Payload.ObservedAt {
					continue
				}
				item.Payload.ObservedAt = normalized
				changed = true
			}
			if changed {
				_ = s.saveQueueLocked()
			}
		}
	}
	return s
}

func (s *Service) configPath() string { return filepath.Join(s.dir, "config.json") }
func (s *Service) queuePath() string  { return filepath.Join(s.dir, "queue.json") }

func publicConfig(c APIConfig) PublicConfig {
	return PublicConfig{Revision: c.Revision, Enabled: c.Enabled, Endpoint: c.Endpoint, KeyConfigured: c.APIKey != "", APIKey: c.APIKey}
}

func normalizeEndpoint(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.RawQuery != "" || u.Fragment != "" || u.User != nil {
		return "", errors.New("APIエンドポイントを確認してください")
	}
	if u.Scheme != "https" {
		return "", errors.New("APIエンドポイントにはHTTPSを指定してください")
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/"
	return u.String(), nil
}

func (s *Service) Configure(expected int, enabled bool, endpoint string, key *string) (PublicConfig, error) {
	normalized, err := normalizeEndpoint(endpoint)
	if err != nil {
		return PublicConfig{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if expected != s.cfg.Revision {
		return PublicConfig{}, errors.New("revision conflict")
	}
	next := s.cfg
	next.Revision++
	next.Enabled, next.Endpoint = enabled, normalized
	if key != nil {
		next.APIKey = strings.TrimSpace(*key)
	}
	if enabled && (next.Endpoint == "" || next.APIKey == "") {
		return PublicConfig{}, errors.New("送信を有効にするにはエンドポイントと認証キーが必要です")
	}
	if err := config.Atomic(s.configPath(), next); err != nil {
		return PublicConfig{}, err
	}
	credentialsChanged := next.Endpoint != s.cfg.Endpoint || next.APIKey != s.cfg.APIKey
	s.cfg = next
	if credentialsChanged {
		now := s.clock().UTC()
		for i := range s.queue {
			if s.queue[i].State == "blocked" {
				s.queue[i].State, s.queue[i].LastError, s.queue[i].HTTPStatus = "pending", "", 0
				s.queue[i].NextAttempt = &now
			}
		}
		if err := s.saveQueueLocked(); err != nil {
			return PublicConfig{}, err
		}
	}
	s.signal()
	return publicConfig(s.cfg), nil
}

func (s *Service) Config() PublicConfig {
	s.mu.Lock()
	defer s.mu.Unlock()
	return publicConfig(s.cfg)
}

func uuidV4() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	h := hex.EncodeToString(b)
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32], nil
}

func payloadFor(record plate.Record, eventID string) SightingPayload {
	p := SightingPayload{EventID: eventID, ObservedAt: record.FirstSeen.Format(apiTimeLayout), Plate: Plate{
		SerialNumber: record.Reading.Number, Region: record.Reading.Region,
		ClassificationNumber: record.Reading.Class, Hiragana: record.Reading.Kana,
	}}
	// An interpolated local position is useful in the NP1 UI but must not be
	// represented to the sharing API as a measured GNSS fix.
	if record.Location != nil && !record.Location.Estimated {
		p.Location = &Location{Latitude: record.Location.Lat, Longitude: record.Location.Lon, AccuracyM: record.Location.AccuracyM}
	}
	return p
}

// Enqueue persists a confirmed local record before allowing the worker to
// transmit it. Repeated calls for the same merged record keep the event ID.
func (s *Service) Enqueue(record plate.Record) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.cfg.Enabled {
		return nil
	}
	for i := range s.queue {
		if s.queue[i].RecordID == record.ID {
			// Before the first attempt, later OCR frames may fill fields. Once an
			// attempt occurred the POST body must remain byte-for-byte stable.
			if s.queue[i].Attempts == 0 {
				s.queue[i].Payload = payloadFor(record, s.queue[i].EventID)
				return s.saveQueueLocked()
			}
			return nil
		}
	}
	id, err := uuidV4()
	if err != nil {
		return err
	}
	now := s.clock().UTC()
	s.queue = append(s.queue, QueueItem{EventID: id, RecordID: record.ID, Payload: payloadFor(record, id), State: "pending", QueuedAt: now, NextAttempt: &now})
	if err := s.saveQueueLocked(); err != nil {
		s.queue = s.queue[:len(s.queue)-1]
		return err
	}
	s.signal()
	return nil
}

// EnqueueUpload adds an explicitly selected historical range to the durable
// queue. Manual uploads are transmitted even when realtime sharing is off;
// realtime detections remain opt-in through APIConfig.Enabled.
func (s *Service) EnqueueUpload(records []plate.Record) (UploadResult, error) {
	result := UploadResult{Total: len(records)}
	s.mu.Lock()
	if s.cfg.Endpoint == "" || s.cfg.APIKey == "" {
		s.mu.Unlock()
		return result, errors.New("NumberShare APIが未設定です")
	}
	existing := make(map[string]int, len(s.queue))
	for i, item := range s.queue {
		existing[item.RecordID] = i
	}
	original := append([]QueueItem(nil), s.queue...)
	now := s.clock().UTC()
	for _, record := range records {
		if strings.TrimSpace(record.Reading.Number) == "" {
			result.Skipped++
			continue
		}
		if index, ok := existing[record.ID]; ok {
			result.Existing++
			item := &s.queue[index]
			if item.State == "blocked" {
				item.Payload = payloadFor(record, item.EventID)
				item.State, item.LastError, item.HTTPStatus, item.Manual = "pending", "", 0, true
				item.NextAttempt = &now
				result.Requeued++
			}
			continue
		}
		id, err := uuidV4()
		if err != nil {
			s.queue = original
			s.mu.Unlock()
			return result, err
		}
		s.queue = append(s.queue, QueueItem{EventID: id, RecordID: record.ID, Payload: payloadFor(record, id), State: "pending", QueuedAt: now, NextAttempt: &now, Manual: true})
		existing[record.ID] = len(s.queue) - 1
		result.Queued++
	}
	if result.Queued > 0 || result.Requeued > 0 {
		if err := s.saveQueueLocked(); err != nil {
			s.queue = original
			s.mu.Unlock()
			return result, err
		}
	}
	s.mu.Unlock()
	if result.Queued > 0 || result.Requeued > 0 {
		s.signal()
	}
	return result, nil
}

func (s *Service) saveQueueLocked() error { return config.Atomic(s.queuePath(), s.queue) }
func (s *Service) signal() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func (s *Service) Status() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := Status{Config: publicConfig(s.cfg), Items: make([]QueueItem, len(s.queue))}
	for _, item := range s.queue {
		switch item.State {
		case "sent":
			out.Sent++
		case "blocked":
			out.Blocked++
		default:
			out.Pending++
		}
	}
	// Return newest first and cap status detail while keeping aggregate counts.
	for i := range out.Items {
		out.Items[i] = s.queue[len(s.queue)-1-i]
	}
	if len(out.Items) > 100 {
		out.Items = out.Items[:100]
	}
	return out
}

func (s *Service) Retry(eventID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.queue {
		if s.queue[i].EventID == eventID && s.queue[i].State != "sent" {
			now := s.clock().UTC()
			s.queue[i].State, s.queue[i].LastError, s.queue[i].HTTPStatus = "pending", "", 0
			s.queue[i].NextAttempt = &now
			if err := s.saveQueueLocked(); err != nil {
				return err
			}
			s.signal()
			return nil
		}
	}
	return errors.New("再送対象が見つかりません")
}

func retryDelay(attempt int) time.Duration {
	d := 5 * time.Second
	for i := 1; i < attempt && d < 15*time.Minute; i++ {
		d *= 2
	}
	if d > 15*time.Minute {
		d = 15 * time.Minute
	}
	return d
}

func (s *Service) nextDue() (QueueItem, APIConfig, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cfg.Endpoint == "" || s.cfg.APIKey == "" {
		return QueueItem{}, APIConfig{}, false
	}
	now := s.clock().UTC()
	for _, item := range s.queue {
		if item.State == "pending" && (s.cfg.Enabled || item.Manual) && (item.NextAttempt == nil || !item.NextAttempt.After(now)) {
			return item, s.cfg, true
		}
	}
	return QueueItem{}, APIConfig{}, false
}

func (s *Service) complete(item QueueItem, status int, body []byte, requestErr error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.queue {
		q := &s.queue[i]
		if q.EventID != item.EventID || q.State != "pending" {
			continue
		}
		now := s.clock().UTC()
		q.Attempts++
		q.LastAttempt, q.HTTPStatus = &now, status
		q.NextAttempt = nil
		var response struct {
			Saved bool `json:"saved"`
			Match struct {
				Status string `json:"status"`
			} `json:"match"`
			Error string `json:"error"`
		}
		_ = json.Unmarshal(body, &response)
		if requestErr == nil && status >= 200 && status < 300 && response.Saved {
			q.State, q.SentAt, q.LastError, q.MatchStatus = "sent", &now, "", response.Match.Status
		} else if requestErr == nil && ((status >= 200 && status < 300) || status == 401 || status == 403 || status == 404 || status == 409 || status == 410 || status == 413 || status == 415 || status == 422) {
			q.State = "blocked"
			q.LastError = apiError(status, response.Error)
		} else {
			q.State = "pending"
			next := now.Add(retryDelay(q.Attempts))
			q.NextAttempt = &next
			if requestErr != nil {
				q.LastError = "APIへ接続できません"
			} else {
				q.LastError = apiError(status, response.Error)
			}
		}
		_ = s.saveQueueLocked()
		return
	}
}

func apiError(status int, detail string) string {
	if detail != "" {
		return fmt.Sprintf("API %d: %s", status, detail)
	}
	if status == 0 {
		return "APIへ接続できません"
	}
	return fmt.Sprintf("APIがHTTP %dを返しました", status)
}

func endpointURL(base, relative string) (string, error) {
	u, err := url.Parse(base)
	if err != nil {
		return "", err
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/" + strings.TrimLeft(relative, "/")
	return u.String(), nil
}

func (s *Service) send(ctx context.Context, cfg APIConfig, item QueueItem) (int, []byte, error) {
	b, err := json.Marshal(item.Payload)
	if err != nil {
		return 0, nil, err
	}
	target, err := endpointURL(cfg.Endpoint, "sightings")
	if err != nil {
		return 0, nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(b))
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-API-Key", cfg.APIKey)
	resp, err := s.client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return resp.StatusCode, nil, err
	}
	if len(body) > maxResponseBytes {
		return resp.StatusCode, nil, errors.New("API response too large")
	}
	return resp.StatusCode, body, nil
}

func (s *Service) Run(ctx context.Context) {
	for {
		for {
			item, cfg, ok := s.nextDue()
			if !ok {
				break
			}
			requestCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
			status, body, err := s.send(requestCtx, cfg, item)
			cancel()
			s.complete(item, status, body, err)
		}
		timer := time.NewTimer(time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-s.wake:
			timer.Stop()
		case <-timer.C:
		}
	}
}

type SharedSighting struct {
	ID                  int64     `json:"id"`
	EventID             string    `json:"event_id"`
	ObservedAt          string    `json:"observed_at"`
	ReceivedAt          string    `json:"received_at"`
	PlateID             *int64    `json:"plate_id"`
	MatchStatus         string    `json:"match_status"`
	RecognitionRevision int       `json:"recognition_revision"`
	Plate               Plate     `json:"plate"`
	Location            *Location `json:"location"`
	Mine                bool      `json:"mine"`
}

type SharedQuery struct {
	Text, MatchStatus, ObservedFrom, ObservedTo string
	Scope                                       string
}

type SharedResponse struct {
	Items     []SharedSighting `json:"items"`
	Truncated bool             `json:"truncated"`
	SyncedAt  time.Time        `json:"syncedAt"`
}

func (s *Service) apiGET(ctx context.Context, relative string, query url.Values, into any) error {
	s.mu.Lock()
	cfg := s.cfg
	s.mu.Unlock()
	if cfg.Endpoint == "" || cfg.APIKey == "" {
		return errors.New("NumberShare APIが未設定です")
	}
	target, err := endpointURL(cfg.Endpoint, relative)
	if err != nil {
		return err
	}
	u, _ := url.Parse(target)
	u.RawQuery = query.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-API-Key", cfg.APIKey)
	resp, err := s.client.Do(req)
	if err != nil {
		return errors.New("NumberShare APIへ接続できません")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var v struct {
			Error string `json:"error"`
		}
		_ = json.NewDecoder(io.LimitReader(resp.Body, maxResponseBytes)).Decode(&v)
		return errors.New(apiError(resp.StatusCode, v.Error))
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxResponseBytes)).Decode(into); err != nil {
		return errors.New("NumberShare APIの応答を読み取れません")
	}
	return nil
}

func containsFold(values []string, q string) bool {
	if q == "" {
		return true
	}
	q = strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(strings.TrimSpace(q), "-", ""), "・", ""))
	for _, value := range values {
		v := strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(value, "-", ""), "・", ""))
		if strings.Contains(v, q) {
			return true
		}
	}
	return false
}

func (s *Service) Shared(ctx context.Context, q SharedQuery) (SharedResponse, error) {
	s.mu.Lock()
	mine := map[string]bool{}
	for _, item := range s.queue {
		mine[item.EventID] = true
	}
	s.mu.Unlock()
	values := url.Values{"limit": {"100"}}
	if q.MatchStatus != "" {
		values.Set("match_status", q.MatchStatus)
	}
	if q.ObservedFrom != "" {
		values.Set("observed_from", q.ObservedFrom)
	}
	if q.ObservedTo != "" {
		values.Set("observed_to", q.ObservedTo)
	}
	out := SharedResponse{Items: []SharedSighting{}, SyncedAt: s.clock().UTC()}
	seenCursors := map[string]bool{}
	for {
		var result struct {
			Items      []SharedSighting `json:"items"`
			NextCursor *string          `json:"next_cursor"`
		}
		if err := s.apiGET(ctx, "sightings", values, &result); err != nil {
			return out, err
		}
		for _, item := range result.Items {
			item.Mine = mine[item.EventID]
			if q.Scope == "mine" && !item.Mine || q.Scope == "others" && item.Mine {
				continue
			}
			if !containsFold([]string{item.Plate.SerialNumber, item.Plate.Region, item.Plate.ClassificationNumber, item.Plate.Hiragana}, q.Text) {
				continue
			}
			out.Items = append(out.Items, item)
		}
		if result.NextCursor == nil || *result.NextCursor == "" {
			return out, nil
		}
		if seenCursors[*result.NextCursor] {
			return out, errors.New("NumberShare APIのページ情報が循環しています")
		}
		seenCursors[*result.NextCursor] = true
		values.Set("cursor", *result.NextCursor)
	}
}

type Vehicle struct {
	ID                   int64     `json:"id"`
	SerialNumber         string    `json:"serial_number"`
	Region               string    `json:"region"`
	ClassificationNumber string    `json:"classification_number"`
	Hiragana             string    `json:"hiragana"`
	Category             string    `json:"category"`
	Comment              string    `json:"comment"`
	Sightings            int       `json:"sightings"`
	LastSeen             *string   `json:"last_seen"`
	LastLocation         *Location `json:"last_location"`
}

type VehicleResponse struct {
	Items      []Vehicle `json:"items"`
	Total      int       `json:"total"`
	Categories []string  `json:"categories"`
}

func (s *Service) Vehicles(ctx context.Context, text, serial, category string) (VehicleResponse, error) {
	q := url.Values{"limit": {"100"}, "offset": {"0"}}
	if text != "" {
		q.Set("q", text)
	}
	if serial != "" {
		q.Set("serial_number", serial)
	}
	if category != "" {
		q.Set("category", category)
	}
	var out VehicleResponse
	err := s.apiGET(ctx, "plates", q, &out)
	return out, err
}

func (s *Service) Check(ctx context.Context) (map[string]any, error) {
	var out map[string]any
	err := s.apiGET(ctx, "users/me", url.Values{}, &out)
	if err != nil {
		return nil, err
	}
	// Never pass unexpected secret fields from an upstream response to the browser.
	for _, key := range []string{"api_key", "recovery_code", "key"} {
		delete(out, key)
	}
	return out, nil
}
