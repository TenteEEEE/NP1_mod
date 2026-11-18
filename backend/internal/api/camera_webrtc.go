package api

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"time"
)

type cameraWebRTCSignal struct {
	Type      string  `json:"type"`
	SDP       string  `json:"sdp"`
	PeerID    string  `json:"peerId,omitempty"`
	Width     int     `json:"width,omitempty"`
	Height    int     `json:"height,omitempty"`
	FPS       float64 `json:"fps,omitempty"`
	Transport string  `json:"transport,omitempty"`
}

func (s *Server) closeCameraWebRTC(id string) {
	if id == "" {
		return
	}
	request, err := http.NewRequest(http.MethodDelete, "http://broker/camera/webrtc/"+url.PathEscape(id), nil)
	if err == nil {
		response, requestErr := s.client.Do(request)
		if requestErr == nil {
			_ = response.Body.Close()
		}
	}
}

func (s *Server) cameraWebRTC(w http.ResponseWriter, r *http.Request) {
	owner, _ := s.auth.Get(r)
	id := r.PathValue("id")
	var offer cameraWebRTCSignal
	if !decode(w, r, &offer) {
		return
	}
	if offer.Type != "offer" || offer.SDP == "" || len(offer.SDP) > 120<<10 {
		fail(w, http.StatusBadRequest, "INVALID_WEBRTC_OFFER", "映像接続を開始できません")
		return
	}
	s.mu.Lock()
	camera, ok := s.cameras[id]
	if !ok || camera.Owner != owner.CSRF || time.Now().After(camera.Expires) {
		s.mu.Unlock()
		fail(w, http.StatusForbidden, "CAMERA_SESSION_EXPIRED", "配信を再開してください")
		return
	}
	oldPeer := camera.PeerID
	s.mu.Unlock()

	payload, _ := json.Marshal(map[string]string{"type": offer.Type, "sdp": offer.SDP})
	request, err := http.NewRequestWithContext(r.Context(), http.MethodPost, "http://broker/camera/webrtc?source="+url.QueryEscape(camera.Source), bytes.NewReader(payload))
	if err != nil {
		fail(w, http.StatusInternalServerError, "CAMERA_REQUEST_FAILED", "映像接続を開始できません")
		return
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := s.client.Do(request)
	if err != nil {
		fail(w, http.StatusServiceUnavailable, "CAMERA_WEBRTC_UNAVAILABLE", "カメラ出力を待っています")
		return
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		fail(w, response.StatusCode, "CAMERA_WEBRTC_UNAVAILABLE", "映像接続を開始できません")
		return
	}
	var answer cameraWebRTCSignal
	if err := json.NewDecoder(io.LimitReader(response.Body, 256<<10)).Decode(&answer); err != nil || answer.Type != "answer" || answer.SDP == "" || answer.PeerID == "" || answer.Transport != "webrtc-udp-h264" {
		fail(w, http.StatusBadGateway, "CAMERA_WEBRTC_INVALID", "映像接続の応答を確認できません")
		return
	}
	s.mu.Lock()
	current, exists := s.cameras[id]
	if !exists || current.Owner != owner.CSRF {
		s.mu.Unlock()
		s.closeCameraWebRTC(answer.PeerID)
		fail(w, http.StatusForbidden, "CAMERA_SESSION_EXPIRED", "配信を再開してください")
		return
	}
	current.PeerID = answer.PeerID
	current.Active = 1
	current.Expires = time.Now().Add(15 * time.Minute)
	s.cameras[id] = current
	s.mu.Unlock()
	if oldPeer != "" && oldPeer != answer.PeerID {
		s.closeCameraWebRTC(oldPeer)
	}
	// The broker peer ID is internal; the browser only needs the SDP answer and
	// observed stream shape.
	answer.PeerID = ""
	jsonOut(w, http.StatusOK, answer)
}
