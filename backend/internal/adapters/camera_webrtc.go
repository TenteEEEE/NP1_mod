package adapters

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/pion/webrtc/v4"
	"github.com/pion/webrtc/v4/pkg/media"
)

const (
	cameraWebRTCMinPort = 50000
	cameraWebRTCMaxPort = 50019
)

type cameraWebRTCPeer struct {
	connection      *webrtc.PeerConnection
	cancel          context.CancelFunc
	source          string
	once            sync.Once
	stateMu         sync.Mutex
	disconnectTimer *time.Timer
}

var cameraWebRTCPeers = struct {
	sync.Mutex
	items map[string]*cameraWebRTCPeer
}{items: make(map[string]*cameraWebRTCPeer)}

func stopCameraWebRTCPeer(peer *cameraWebRTCPeer) {
	if peer == nil {
		return
	}
	peer.once.Do(func() {
		peer.stateMu.Lock()
		if peer.disconnectTimer != nil {
			peer.disconnectTimer.Stop()
			peer.disconnectTimer = nil
		}
		peer.stateMu.Unlock()
		peer.cancel()
		_ = peer.connection.Close()
	})
}

func cameraWebRTCID() (string, error) {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return hex.EncodeToString(value), nil
}

func cameraAVCCToAnnexB(data []byte) ([]byte, error) {
	result := make([]byte, 0, len(data)+32)
	for len(data) != 0 {
		if len(data) < 4 {
			return nil, errors.New("truncated AVC sample")
		}
		size := int(binary.BigEndian.Uint32(data[:4]))
		data = data[4:]
		if size <= 0 || size > len(data) {
			return nil, errors.New("invalid AVC NAL length")
		}
		result = append(result, 0, 0, 0, 1)
		result = append(result, data[:size]...)
		data = data[size:]
	}
	return result, nil
}

func closeCameraWebRTCPeer(id string) {
	cameraWebRTCPeers.Lock()
	peer := cameraWebRTCPeers.items[id]
	delete(cameraWebRTCPeers.items, id)
	cameraWebRTCPeers.Unlock()
	stopCameraWebRTCPeer(peer)
}

func cameraWebRTCStream(ctx context.Context, hub *cameraEncoderHub, track *webrtc.TrackLocalStaticSample, duration time.Duration) {
	var cursor uint64
	for {
		_, frames, notify, gap := hub.after(cursor)
		if gap {
			// UDP/RTP may discard dependent frames without holding a TCP queue.
			// Restart from the newest cached IDR on the same PeerConnection.
			cursor = 0
			continue
		}
		if len(frames) == 0 {
			select {
			case <-notify:
				continue
			case <-ctx.Done():
				return
			}
		}
		for _, frame := range frames {
			annexB, err := cameraAVCCToAnnexB(frame.sample.Data)
			if err != nil {
				cursor = frame.id
				continue
			}
			if err := track.WriteSample(media.Sample{Data: annexB, Duration: duration}); err != nil {
				return
			}
			cursor = frame.id
		}
	}
}

func HandleCameraWebRTC(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodDelete {
		closeCameraWebRTCPeer(r.PathValue("id"))
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodPost || len(r.URL.Query()) != 1 || len(r.URL.Query()["source"]) != 1 {
		http.Error(w, "invalid WebRTC request", http.StatusBadRequest)
		return
	}
	source := r.URL.Query().Get("source")
	hub := cameraEncoderHubs[source]
	if hub == nil {
		http.Error(w, "invalid camera source", http.StatusBadRequest)
		return
	}
	var offer webrtc.SessionDescription
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 128<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&offer); err != nil || decoder.Decode(&struct{}{}) != io.EOF || offer.Type != webrtc.SDPTypeOffer || offer.SDP == "" {
		http.Error(w, "invalid WebRTC offer", http.StatusBadRequest)
		return
	}

	hub.start()
	ready, cancelReady := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancelReady()
	var configReady bool
	var width, height uint16
	var fps float64
	var profile string
	for !configReady {
		config, frames, notify, _ := hub.after(0)
		if len(frames) != 0 && len(config.AVC) >= 4 {
			width, height = config.Width, config.Height
			fps = float64(config.Timescale) / float64(config.SampleDuration)
			profile = fmt.Sprintf("%02x%02x%02x", config.AVC[1], config.AVC[2], config.AVC[3])
			configReady = true
			break
		}
		select {
		case <-notify:
		case <-ready.Done():
			http.Error(w, "current encoder unavailable", http.StatusServiceUnavailable)
			return
		}
	}

	mediaEngine := &webrtc.MediaEngine{}
	if err := mediaEngine.RegisterDefaultCodecs(); err != nil {
		http.Error(w, "WebRTC codec initialization failed", http.StatusInternalServerError)
		return
	}
	settingEngine := webrtc.SettingEngine{}
	settingEngine.SetNetworkTypes([]webrtc.NetworkType{webrtc.NetworkTypeUDP4})
	if err := settingEngine.SetEphemeralUDPPortRange(cameraWebRTCMinPort, cameraWebRTCMaxPort); err != nil {
		http.Error(w, "WebRTC port initialization failed", http.StatusInternalServerError)
		return
	}
	api := webrtc.NewAPI(webrtc.WithMediaEngine(mediaEngine), webrtc.WithSettingEngine(settingEngine))
	connection, err := api.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		http.Error(w, "WebRTC initialization failed", http.StatusInternalServerError)
		return
	}
	track, err := webrtc.NewTrackLocalStaticSample(webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeH264, ClockRate: 90000, SDPFmtpLine: "level-asymmetry-allowed=1;packetization-mode=1;profile-level-id=" + profile}, "video", source)
	if err != nil {
		_ = connection.Close()
		http.Error(w, "WebRTC track initialization failed", http.StatusInternalServerError)
		return
	}
	sender, err := connection.AddTrack(track)
	if err != nil {
		_ = connection.Close()
		http.Error(w, "WebRTC track negotiation failed", http.StatusBadRequest)
		return
	}
	go func() {
		buffer := make([]byte, 1500)
		for {
			if _, _, err := sender.Read(buffer); err != nil {
				return
			}
		}
	}()
	if err := connection.SetRemoteDescription(offer); err != nil {
		_ = connection.Close()
		http.Error(w, "WebRTC offer was rejected", http.StatusBadRequest)
		return
	}
	answer, err := connection.CreateAnswer(nil)
	if err != nil {
		_ = connection.Close()
		http.Error(w, "WebRTC answer failed", http.StatusInternalServerError)
		return
	}
	gathered := webrtc.GatheringCompletePromise(connection)
	if err := connection.SetLocalDescription(answer); err != nil {
		_ = connection.Close()
		http.Error(w, "WebRTC answer failed", http.StatusInternalServerError)
		return
	}
	select {
	case <-gathered:
	case <-r.Context().Done():
		_ = connection.Close()
		return
	case <-time.After(5 * time.Second):
		_ = connection.Close()
		http.Error(w, "WebRTC candidate gathering timed out", http.StatusGatewayTimeout)
		return
	}
	local := connection.LocalDescription()
	if local == nil {
		_ = connection.Close()
		http.Error(w, "WebRTC answer unavailable", http.StatusInternalServerError)
		return
	}
	id, err := cameraWebRTCID()
	if err != nil {
		_ = connection.Close()
		http.Error(w, "WebRTC peer ID failed", http.StatusInternalServerError)
		return
	}
	cameraWebRTCPeers.Lock()
	streamContext, cancelStream := context.WithCancel(context.Background())
	peer := &cameraWebRTCPeer{connection: connection, cancel: cancelStream, source: source}
	replaced := make([]*cameraWebRTCPeer, 0, 1)
	for existingID, existing := range cameraWebRTCPeers.items {
		if existing.source == source {
			delete(cameraWebRTCPeers.items, existingID)
			replaced = append(replaced, existing)
		}
	}
	if len(cameraWebRTCPeers.items) >= 2 {
		cameraWebRTCPeers.Unlock()
		stopCameraWebRTCPeer(peer)
		http.Error(w, "camera peers busy", http.StatusTooManyRequests)
		return
	}
	cameraWebRTCPeers.items[id] = peer
	cameraWebRTCPeers.Unlock()
	for _, existing := range replaced {
		stopCameraWebRTCPeer(existing)
	}
	connection.OnICEConnectionStateChange(func(state webrtc.ICEConnectionState) {
		peer.stateMu.Lock()
		if peer.disconnectTimer != nil {
			peer.disconnectTimer.Stop()
			peer.disconnectTimer = nil
		}
		if state == webrtc.ICEConnectionStateDisconnected {
			peer.disconnectTimer = time.AfterFunc(5*time.Second, func() { closeCameraWebRTCPeer(id) })
		}
		peer.stateMu.Unlock()
		if state == webrtc.ICEConnectionStateFailed || state == webrtc.ICEConnectionStateClosed {
			closeCameraWebRTCPeer(id)
		}
	})
	duration := time.Duration(float64(time.Second) / fps)
	go cameraWebRTCStream(streamContext, hub, track, duration)
	go func() {
		select {
		case <-streamContext.Done():
		case <-time.After(15 * time.Minute):
			closeCameraWebRTCPeer(id)
		}
	}()
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(map[string]any{"type": local.Type, "sdp": local.SDP, "peerId": id, "width": width, "height": height, "fps": fps, "transport": "webrtc-udp-h264"})
}
