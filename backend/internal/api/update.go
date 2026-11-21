package api

import (
	"net/http"
	"time"

	"github.com/np1-control-center/ncc/internal/broker"
	"github.com/np1-control-center/ncc/internal/update"
)

func (s *Server) updateRoutes() {
	client := broker.ClientWithHeaderTimeout(update.Socket, 5*time.Second)
	for _, route := range []struct{ pattern, path, audit string }{
		{"GET /api/v1/system/np1cc-update", "/update", ""},
		{"PUT /api/v1/system/np1cc-update", "/update", "np1cc-update.settings"},
		{"POST /api/v1/system/np1cc-update/check", "/update/check", "np1cc-update.check"},
		{"POST /api/v1/system/np1cc-update/apply", "/update/apply", "np1cc-update.apply"},
	} {
		s.mux.HandleFunc(route.pattern, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "no-store")
			s.forwardControlWithClient(w, r, route.path, route.audit, 512, client)
		})
	}
}
