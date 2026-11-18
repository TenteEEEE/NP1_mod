package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/np1-control-center/ncc/internal/config"
)

func TestCameraWebRTCOfferUsesFixedSourceAndClosesBrokerPeer(t *testing.T) {
	s := New(config.Config{NetworkTrust: true, StateDir: t.TempDir()})
	posts, deletes := 0, 0
	s.client.Transport = stockAuditTransport(func(r *http.Request) (*http.Response, error) {
		switch r.Method {
		case http.MethodPost:
			posts++
			if r.URL.Host != "broker" || r.URL.Path != "/camera/webrtc" || r.URL.RawQuery != "source=front" {
				t.Fatalf("unfixed WebRTC target: %s", r.URL)
			}
			body, _ := io.ReadAll(r.Body)
			if !strings.Contains(string(body), "synthetic-offer") {
				t.Fatalf("offer not forwarded: %s", body)
			}
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"type":"answer","sdp":"synthetic-answer","peerId":"broker-peer","width":1920,"height":1080,"fps":27,"transport":"webrtc-udp-h264"}`))}, nil
		case http.MethodDelete:
			deletes++
			if r.URL.Path != "/camera/webrtc/broker-peer" {
				t.Fatalf("wrong peer closed: %s", r.URL.Path)
			}
			return &http.Response{StatusCode: 204, Body: io.NopCloser(strings.NewReader(""))}, nil
		default:
			t.Fatalf("unexpected broker method: %s", r.Method)
			return nil, nil
		}
	})
	server := httptest.NewServer(s.Handler())
	defer server.Close()
	client := server.Client()
	client.Jar, _ = cookiejar.New(nil)
	r := request(t, client, server, "GET", "/auth/session", "", nil)
	var session struct{ CSRF string }
	_ = json.NewDecoder(r.Body).Decode(&session)
	r.Body.Close()
	r = request(t, client, server, "POST", "/camera/sessions", session.CSRF, map[string]string{"source": "front"})
	var camera struct{ ID string }
	_ = json.NewDecoder(r.Body).Decode(&camera)
	r.Body.Close()
	r = request(t, client, server, "POST", "/camera/sessions/"+camera.ID+"/webrtc", session.CSRF, map[string]string{"type": "offer", "sdp": "synthetic-offer"})
	var answer cameraWebRTCSignal
	_ = json.NewDecoder(r.Body).Decode(&answer)
	r.Body.Close()
	if r.StatusCode != 200 || answer.SDP != "synthetic-answer" || answer.PeerID != "" || answer.Transport != "webrtc-udp-h264" || posts != 1 {
		t.Fatalf("WebRTC answer = %d %#v posts=%d", r.StatusCode, answer, posts)
	}
	// A new view of the same source replaces an abandoned browser session
	// immediately instead of leaving its peer to consume the two-camera limit.
	r = request(t, client, server, "POST", "/camera/sessions", session.CSRF, map[string]string{"source": "front"})
	var replacement struct{ ID string }
	_ = json.NewDecoder(r.Body).Decode(&replacement)
	r.Body.Close()
	if r.StatusCode != http.StatusCreated || replacement.ID == "" || deletes != 1 {
		t.Fatalf("replacement session = %d %#v deletes=%d", r.StatusCode, replacement, deletes)
	}
	r = request(t, client, server, "DELETE", "/camera/sessions/"+camera.ID, session.CSRF, nil)
	r.Body.Close()
	if r.StatusCode != 200 || deletes != 1 {
		t.Fatalf("replaced session cleanup = %d deletes=%d", r.StatusCode, deletes)
	}
	r = request(t, client, server, "POST", "/camera/sessions/"+replacement.ID+"/webrtc", session.CSRF, map[string]string{"type": "offer", "sdp": "synthetic-offer"})
	r.Body.Close()
	r = request(t, client, server, "DELETE", "/camera/sessions/"+replacement.ID, session.CSRF, nil)
	r.Body.Close()
	if r.StatusCode != 200 || posts != 2 || deletes != 2 {
		t.Fatalf("replacement peer cleanup = %d posts=%d deletes=%d", r.StatusCode, posts, deletes)
	}
}
