package api

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/np1-control-center/ncc/internal/numbershare"
	"github.com/np1-control-center/ncc/internal/plate"
)

func strictQuery(w http.ResponseWriter, r *http.Request, allowed ...string) (url.Values, bool) {
	set := map[string]bool{}
	for _, key := range allowed {
		set[key] = true
	}
	q := r.URL.Query()
	for key, values := range q {
		if !set[key] || len(values) != 1 {
			fail(w, http.StatusBadRequest, "INVALID_QUERY", "検索条件を確認してください")
			return nil, false
		}
	}
	return q, true
}

func numberShareFailure(w http.ResponseWriter, err error) {
	if err == nil {
		return
	}
	message := err.Error()
	status, code := http.StatusBadGateway, "NUMBER_SHARE_UNAVAILABLE"
	if strings.Contains(message, "未設定") {
		status, code = http.StatusConflict, "NUMBER_SHARE_NOT_CONFIGURED"
	} else if strings.Contains(message, "revision conflict") {
		status, code = http.StatusConflict, "REVISION_CONFLICT"
	} else if strings.Contains(message, "HTTPS") || strings.Contains(message, "エンドポイント") || strings.Contains(message, "認証キー") || strings.Contains(message, "キー") || strings.Contains(message, "匿名ユーザーID") || strings.Contains(message, "端末名") || strings.Contains(message, "件数") {
		status, code = http.StatusBadRequest, "INVALID_NUMBER_SHARE_CONFIG"
	}
	fail(w, status, code, message)
}

func (s *Server) registerNumberShare() {
	n := s.numberShare
	s.mux.HandleFunc("GET /api/v1/plate/share/status", func(w http.ResponseWriter, r *http.Request) {
		jsonOut(w, http.StatusOK, n.Status())
	})
	s.mux.HandleFunc("PUT /api/v1/plate/share/config", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			ExpectedRevision int     `json:"expectedRevision"`
			Enabled          bool    `json:"enabled"`
			Endpoint         string  `json:"endpoint"`
			Key              *string `json:"key"`
		}
		if !decode(w, r, &body) {
			return
		}
		cfg, err := n.Configure(body.ExpectedRevision, body.Enabled, body.Endpoint, body.Key)
		if err != nil {
			numberShareFailure(w, err)
			return
		}
		s.audit("numbershare.config.update", "accepted")
		jsonOut(w, http.StatusOK, cfg)
	})
	s.mux.HandleFunc("PUT /api/v1/plate/share/management", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			ExpectedRevision int     `json:"expectedRevision"`
			ManagementURL    string  `json:"managementUrl"`
			Key              *string `json:"key"`
		}
		if !decode(w, r, &body) {
			return
		}
		cfg, err := n.ConfigureManagement(body.ExpectedRevision, body.ManagementURL, body.Key)
		if err != nil {
			numberShareFailure(w, err)
			return
		}
		s.audit("numbershare.management.update", "accepted")
		jsonOut(w, http.StatusOK, cfg)
	})
	s.mux.HandleFunc("POST /api/v1/plate/share/register", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			ExpectedRevision int    `json:"expectedRevision"`
			Endpoint         string `json:"endpoint"`
			UserHandle       string `json:"userHandle"`
			DeviceName       string `json:"deviceName"`
			BootstrapKey     string `json:"bootstrapKey"`
		}
		if !decode(w, r, &body) {
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
		defer cancel()
		result, err := n.Register(ctx, numbershare.RegistrationRequest{
			ExpectedRevision: body.ExpectedRevision, Endpoint: body.Endpoint, UserHandle: body.UserHandle,
			DeviceName: body.DeviceName, BootstrapKey: body.BootstrapKey,
		})
		if err != nil {
			numberShareFailure(w, err)
			return
		}
		s.audit("numbershare.registration", "accepted")
		jsonOut(w, http.StatusCreated, result)
	})
	s.mux.HandleFunc("POST /api/v1/plate/share/retry/{event_id}", func(w http.ResponseWriter, r *http.Request) {
		if err := n.Retry(r.PathValue("event_id")); err != nil {
			fail(w, http.StatusNotFound, "QUEUE_ITEM_NOT_FOUND", err.Error())
			return
		}
		s.audit("numbershare.retry", "accepted")
		jsonOut(w, http.StatusOK, map[string]bool{"queued": true})
	})
	s.mux.HandleFunc("POST /api/v1/plate/share/check", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		defer cancel()
		identity, err := n.Check(ctx)
		if err != nil {
			numberShareFailure(w, err)
			return
		}
		jsonOut(w, http.StatusOK, map[string]any{"ok": true, "identity": identity})
	})
	s.mux.HandleFunc("POST /api/v1/plate/share/upload", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			From string `json:"from"`
			To   string `json:"to"`
		}
		if !decode(w, r, &body) {
			return
		}
		if strings.TrimSpace(body.From) == "" || strings.TrimSpace(body.To) == "" {
			fail(w, http.StatusBadRequest, "INVALID_PERIOD", "アップロード期間を指定してください")
			return
		}
		records, err := s.plates.store.ListAll(plate.Query{From: body.From, To: body.To})
		if err != nil {
			fail(w, http.StatusBadRequest, "INVALID_PERIOD", err.Error())
			return
		}
		result, err := n.EnqueueUpload(records)
		if err != nil {
			numberShareFailure(w, err)
			return
		}
		s.audit("numbershare.period.upload", "accepted")
		jsonOut(w, http.StatusAccepted, result)
	})
	s.mux.HandleFunc("GET /api/v1/plate/share/sightings", func(w http.ResponseWriter, r *http.Request) {
		q, ok := strictQuery(w, r, "q", "matchStatus", "observedFrom", "observedTo", "scope")
		if !ok {
			return
		}
		scope := q.Get("scope")
		if scope != "" && scope != "all" && scope != "mine" && scope != "others" {
			fail(w, http.StatusBadRequest, "INVALID_QUERY", "表示範囲を確認してください")
			return
		}
		match := q.Get("matchStatus")
		if match != "" && match != "exact" && match != "candidate" && match != "none" {
			fail(w, http.StatusBadRequest, "INVALID_QUERY", "照合状態を確認してください")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
		defer cancel()
		result, err := n.Shared(ctx, numbershare.SharedQuery{Text: q.Get("q"), MatchStatus: match, ObservedFrom: q.Get("observedFrom"), ObservedTo: q.Get("observedTo"), Scope: scope})
		if err != nil {
			numberShareFailure(w, err)
			return
		}
		jsonOut(w, http.StatusOK, result)
	})
	s.mux.HandleFunc("GET /api/v1/plate/share/vehicles", func(w http.ResponseWriter, r *http.Request) {
		q, ok := strictQuery(w, r, "q", "serialNumber", "category")
		if !ok {
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		defer cancel()
		result, err := n.Vehicles(ctx, q.Get("q"), q.Get("serialNumber"), q.Get("category"))
		if err != nil {
			numberShareFailure(w, err)
			return
		}
		jsonOut(w, http.StatusOK, result)
	})
}
