package api

import (
	"net/http"
	"time"

	"github.com/np1-control-center/ncc/internal/broker"
)

func (s *Server) registerWiFiRoutes() {
	s.mux.HandleFunc("GET /api/v1/network/wifi/networks", func(w http.ResponseWriter, r *http.Request) { s.proxy(w, r, "/wifi/networks") })
	s.mux.HandleFunc("GET /api/v1/network/uplink", func(w http.ResponseWriter, r *http.Request) { s.proxy(w, r, "/uplink") })
	s.mux.HandleFunc("PUT /api/v1/network/uplink", func(w http.ResponseWriter, r *http.Request) { s.forwardControl(w, r, "/uplink", "uplink.mode") })
	s.mux.HandleFunc("GET /api/v1/network/hotspot", func(w http.ResponseWriter, r *http.Request) { s.proxy(w, r, "/hotspot") })
	s.mux.HandleFunc("PUT /api/v1/network/hotspot", func(w http.ResponseWriter, r *http.Request) { s.forwardControl(w, r, "/hotspot", "hotspot.update") })
	setupClient := s.client
	if s.cfg.Broker != "" {
		setupClient = broker.ClientWithHeaderTimeout(s.cfg.Broker, 90*time.Second)
	}
	for _, action := range []string{"scan", "connect-new", "connect-saved"} {
		s.mux.HandleFunc("POST /api/v1/network/wifi/"+action, func(w http.ResponseWriter, r *http.Request) {
			s.forwardControlWithClient(w, r, "/wifi/"+action, "wifi."+action, 4096, setupClient)
		})
	}
	for _, action := range []string{"confirm", "rollback"} {
		s.mux.HandleFunc("POST /api/v1/network/wifi/"+action, func(w http.ResponseWriter, r *http.Request) { s.forwardControl(w, r, "/wifi/"+action, "wifi."+action) })
	}
}
