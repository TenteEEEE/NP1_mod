package adapters

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWiFiHTTPOnlyAllowsTypedBoundedOperations(t *testing.T) {
	previous := wifiController
	c, _ := wifiTestController(t)
	wifiController = c
	defer func() { wifiController = previous }()
	mux := http.NewServeMux()
	RegisterWiFiHandlers(mux)
	for _, body := range []string{`{"command":"SAVE_CONFIG"}`, `{"networkID":4,"profileID":"ncc-anything"}`, `{}`, `{"networkID":4} {"networkID":7}`} {
		request := httptest.NewRequest("POST", "/wifi/connect-saved", strings.NewReader(body))
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, request)
		if response.Code != 400 {
			t.Fatalf("unexpected status %d", response.Code)
		}
	}
	request := httptest.NewRequest("POST", "/wifi/connect-new", strings.NewReader(`{"ssid":"Private LAN","security":"wpa2","password":"fixture-private-secret"}`))
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != 200 || strings.Contains(response.Body.String(), "fixture-private-secret") {
		t.Fatal("typed connection failed or returned secret")
	}
	var transaction WiFiTransaction
	if err := json.Unmarshal(response.Body.Bytes(), &transaction); err != nil || transaction.ID == "" {
		t.Fatal("connection transaction missing")
	}
	request = httptest.NewRequest("POST", "/wifi/confirm", strings.NewReader(`{"transactionID":"`+transaction.ID+`"}`))
	response = httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != 200 {
		t.Fatal("connection confirmation failed")
	}
	response = httptest.NewRecorder()
	mux.ServeHTTP(response, httptest.NewRequest("GET", "/wifi/networks", nil))
	var view WiFiView
	if err := json.Unmarshal(response.Body.Bytes(), &view); response.Code != 200 || err != nil || len(view.Saved) != 1 || view.Saved[0].Password != "fixture-private-secret" {
		t.Fatal("owner network list does not contain saved credential")
	}
	response = httptest.NewRecorder()
	mux.ServeHTTP(response, httptest.NewRequest("POST", "/wifi/command", strings.NewReader(`{}`)))
	if response.Code != 404 {
		t.Fatal("arbitrary command endpoint exists")
	}
}
