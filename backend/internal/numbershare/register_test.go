package numbershare

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestDerivedUserHandleIsStableAndDoesNotExposeInternalID(t *testing.T) {
	a := derivedUserHandle("9007199254740993")
	b := derivedUserHandle("9007199254740993")
	if a != b || !strings.HasPrefix(a, "np1_") || len(a) != 24 || strings.Contains(a, "9007199254740993") {
		t.Fatalf("derived handle = %q %q", a, b)
	}
}

func TestEnsureDefersInitialRegistrationToOwnerUIWithoutExplicitHandle(t *testing.T) {
	r := NewRegistrar(RegistrationOptions{ConfigPath: filepath.Join(t.TempDir(), "missing.json")})
	result, err := r.Ensure(context.Background())
	if err != nil || result.Status != "ui-registration-required" || result.Configured {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestEnsureRegistersExplicitOwnerHandleWithoutExposingBootstrapKey(t *testing.T) {
	dir := t.TempDir()
	bootstrap := filepath.Join(dir, "bootstrap.key")
	if err := os.WriteFile(bootstrap, []byte("bootstrap-user-key-0123456789abcdef\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var requestedHandle string
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case "/app/numbershare/api/users":
			if r.Method != http.MethodPost || r.Header.Get("X-API-Key") != "bootstrap-user-key-0123456789abcdef" {
				t.Fatal("registration auth mismatch")
			}
			var body map[string]string
			_ = json.NewDecoder(r.Body).Decode(&body)
			requestedHandle = body["user_handle"]
			return response(http.StatusCreated, `{"user_id":"u1","user_handle":"`+requestedHandle+`","device_id":"d1","api_key":"issued-device-key-0123456789abcdef","recovery_code":"recovery-code-0123456789abcdef"}`), nil
		case "/app/numbershare/api/users/me":
			if r.Header.Get("X-API-Key") != "issued-device-key-0123456789abcdef" {
				t.Fatal("issued key mismatch")
			}
			return response(http.StatusOK, `{"user_id":"u1","user_handle":"`+requestedHandle+`","device_id":"d1"}`), nil
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
			return nil, nil
		}
	})}
	r := NewRegistrar(RegistrationOptions{ConfigPath: filepath.Join(dir, "private", "numbershare.json"), RecoveryPath: filepath.Join(dir, "private", "recovery.json"), BootstrapKeyPath: bootstrap, DeviceName: "NP1 test", UserHandle: "owner_01"})
	r.Client = client
	r.Clock = func() time.Time { return time.Date(2026, 11, 17, 0, 0, 0, 0, time.UTC) }
	result, err := r.Ensure(context.Background())
	if err != nil || !result.Authenticated || result.Created != "user" || result.Status != "ok" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if requestedHandle != "owner_01" {
		t.Fatalf("explicit handle changed: %q", requestedHandle)
	}
	configBytes, _ := os.ReadFile(r.Options.ConfigPath)
	recoveryBytes, _ := os.ReadFile(r.Options.RecoveryPath)
	if strings.Contains(string(configBytes), "bootstrap-user-key") || !strings.Contains(string(configBytes), "issued-device-key") || !strings.Contains(string(recoveryBytes), "recovery-code") {
		t.Fatal("private output mismatch")
	}
	if info, _ := os.Stat(r.Options.ConfigPath); runtime.GOOS != "windows" && info.Mode().Perm() != 0o640 {
		t.Fatalf("config mode=%o", info.Mode().Perm())
	}
	if info, _ := os.Stat(r.Options.RecoveryPath); runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("recovery mode=%o", info.Mode().Perm())
	}
}

func TestEnsureUsesExistingUserKeyForAdditionalDevice(t *testing.T) {
	dir := t.TempDir()
	userKey := filepath.Join(dir, "user.key")
	if err := os.WriteFile(userKey, []byte("existing-user-key-0123456789abcdef"), 0o600); err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		key := r.Header.Get("X-API-Key")
		if r.URL.Path == "/app/numbershare/api/users/me" && key == "existing-user-key-0123456789abcdef" {
			return response(http.StatusOK, `{"user_id":"u1","user_handle":"owner","device_id":"old"}`), nil
		}
		if r.URL.Path == "/app/numbershare/api/devices" && key == "existing-user-key-0123456789abcdef" {
			return response(http.StatusCreated, `{"device_id":"new","api_key":"new-device-key-0123456789abcdef"}`), nil
		}
		if r.URL.Path == "/app/numbershare/api/users/me" && key == "new-device-key-0123456789abcdef" {
			return response(http.StatusOK, `{"user_id":"u1","user_handle":"owner","device_id":"new"}`), nil
		}
		t.Fatalf("unexpected request %s %s", r.URL.Path, key)
		return nil, nil
	})}
	r := NewRegistrar(RegistrationOptions{ConfigPath: filepath.Join(dir, "numbershare.json"), ExistingUserKeyPath: userKey, DeviceName: "np1-02"})
	r.Client = client
	r.InternalUserID = func(context.Context, string) (string, error) {
		t.Fatal("internal ID should not be needed")
		return "", nil
	}
	result, err := r.Ensure(context.Background())
	if err != nil || result.Created != "device" || !result.Authenticated {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func response(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}
}
