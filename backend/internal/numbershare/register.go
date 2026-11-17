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
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/np1-control-center/ncc/internal/config"
)

const defaultTailscaleBinary = "/overlay/owner-apps/np1-control/bin/tailscale"

type RegistrationOptions struct {
	ConfigPath          string
	RecoveryPath        string
	BootstrapKeyPath    string
	ExistingUserKeyPath string
	InternalUserIDPath  string
	DeviceName          string
}

type RegistrationResult struct {
	Configured    bool   `json:"configured"`
	Authenticated bool   `json:"authenticated"`
	Created       string `json:"created,omitempty"`
	Status        string `json:"status"`
}

type registrationIdentity struct {
	UserID     string `json:"user_id"`
	UserHandle string `json:"user_handle"`
	DeviceID   string `json:"device_id"`
}

type registrationResponse struct {
	registrationIdentity
	APIKey       string `json:"api_key"`
	RecoveryCode string `json:"recovery_code"`
}

type Registrar struct {
	Options        RegistrationOptions
	Client         *http.Client
	Clock          func() time.Time
	InternalUserID func(context.Context, string) (string, error)
	Hostname       func() (string, error)
}

func NewRegistrar(options RegistrationOptions) *Registrar {
	return &Registrar{
		Options:        options,
		Client:         registrationHTTPClient(),
		Clock:          time.Now,
		InternalUserID: detectInternalUserID,
		Hostname:       os.Hostname,
	}
}

func registrationHTTPClient() *http.Client {
	client := newHTTPClient()
	client.CheckRedirect = func(*http.Request, []*http.Request) error {
		return errors.New("redirect-refused")
	}
	return client
}

func readCredential(path string, required bool) (string, error) {
	if strings.TrimSpace(path) == "" {
		if required {
			return "", errors.New("credential-path-required")
		}
		return "", nil
	}
	info, err := os.Stat(path)
	if err != nil {
		if !required && errors.Is(err, os.ErrNotExist) {
			return "", nil
		}
		return "", err
	}
	// Windows reports synthesized POSIX mode bits that do not represent its ACLs.
	// The deployed NP1 target is Linux, where group/other access must be rejected.
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		return "", errors.New("credential-permissions-too-broad")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	value := strings.TrimSpace(string(b))
	if len(value) < 24 || len(value) > 512 || strings.ContainsAny(value, "\r\n\x00") {
		return "", errors.New("invalid-credential")
	}
	return value, nil
}

func detectInternalUserID(ctx context.Context, explicitPath string) (string, error) {
	if explicitPath != "" {
		b, err := os.ReadFile(explicitPath)
		if err != nil {
			return "", err
		}
		value := strings.TrimSpace(string(b))
		if value == "" || len(value) > 256 || strings.ContainsAny(value, "\r\n\x00") {
			return "", errors.New("invalid-internal-user-id")
		}
		return value, nil
	}
	for _, binary := range []string{defaultTailscaleBinary, "/usr/bin/tailscale", "/usr/local/bin/tailscale"} {
		if _, err := os.Stat(binary); err != nil {
			continue
		}
		command := exec.CommandContext(ctx, binary, "status", "--json")
		output, err := command.Output()
		if err != nil || len(output) > 2<<20 {
			continue
		}
		var status struct {
			Self struct {
				UserID json.Number `json:"UserID"`
			} `json:"Self"`
		}
		decoder := json.NewDecoder(bytes.NewReader(output))
		decoder.UseNumber()
		if decoder.Decode(&status) == nil {
			value := strings.TrimSpace(status.Self.UserID.String())
			if value != "" && value != "0" {
				return value, nil
			}
		}
	}
	return "", errors.New("internal-user-id-unavailable")
}

func derivedUserHandle(internalID string) string {
	digest := sha256.Sum256([]byte("np1-numbershare-user-v1:" + internalID))
	return "np1_" + hex.EncodeToString(digest[:10])
}

func normalizeDeviceName(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		value = "np1"
	}
	runes := []rune(value)
	if len(runes) > 100 {
		value = string(runes[:100])
	}
	return value
}

func (r *Registrar) request(ctx context.Context, method, endpoint, key string, body any, into any) (int, error) {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return 0, err
		}
		reader = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, reader)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-API-Key", key)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := r.Client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponse+1))
	if err != nil || len(data) > maxResponse {
		return resp.StatusCode, errors.New("invalid-response")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return resp.StatusCode, fmt.Errorf("http-%d", resp.StatusCode)
	}
	if into != nil && json.Unmarshal(data, into) != nil {
		return resp.StatusCode, errors.New("invalid-response")
	}
	return resp.StatusCode, nil
}

func (r *Registrar) validateKey(ctx context.Context, baseURL, key string) (registrationIdentity, error) {
	var identity registrationIdentity
	status, err := r.request(ctx, http.MethodGet, baseURL+"users/me", key, nil, &identity)
	if err != nil || status != http.StatusOK || identity.UserID == "" || identity.DeviceID == "" {
		return registrationIdentity{}, errors.New("credential-validation-failed")
	}
	return identity, nil
}

func writePrivateJSON(path string, value any, mode os.FileMode) error {
	if path == "" {
		return errors.New("private-output-path-required")
	}
	if err := config.Atomic(path, value); err != nil {
		return err
	}
	return os.Chmod(path, mode)
}

func (r *Registrar) configured(ctx context.Context) (bool, error) {
	c, err := loadConfig(r.Options.ConfigPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	if !c.Enabled {
		return false, nil
	}
	_, err = r.validateKey(ctx, c.BaseURL, c.APIKey)
	return err == nil, err
}

func (r *Registrar) Ensure(ctx context.Context) (RegistrationResult, error) {
	if ok, err := r.configured(ctx); ok {
		return RegistrationResult{Configured: true, Authenticated: true, Status: "existing"}, nil
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return RegistrationResult{Status: "existing-invalid"}, err
	}

	baseURL := defaultBaseURL
	deviceName := r.Options.DeviceName
	if deviceName == "" {
		deviceName, _ = r.Hostname()
	}
	deviceName = normalizeDeviceName(deviceName)

	var issued registrationResponse
	created := ""
	existingUserKey, err := readCredential(r.Options.ExistingUserKeyPath, false)
	if err != nil {
		return RegistrationResult{Status: "user-key"}, err
	}
	if existingUserKey != "" {
		source, err := r.validateKey(ctx, baseURL, existingUserKey)
		if err != nil {
			return RegistrationResult{Status: "user-key-invalid"}, err
		}
		status, err := r.request(ctx, http.MethodPost, baseURL+"devices", existingUserKey, map[string]string{"device_name": deviceName}, &issued)
		if err != nil || status != http.StatusCreated {
			return RegistrationResult{Status: "device-registration"}, err
		}
		issued.UserID = source.UserID
		issued.UserHandle = source.UserHandle
		created = "device"
	} else {
		bootstrapKey, err := readCredential(r.Options.BootstrapKeyPath, true)
		if err != nil {
			return RegistrationResult{Status: "bootstrap-key"}, err
		}
		internalID, err := r.InternalUserID(ctx, r.Options.InternalUserIDPath)
		if err != nil {
			return RegistrationResult{Status: "internal-user-id"}, err
		}
		body := map[string]string{"user_handle": derivedUserHandle(internalID), "device_name": deviceName}
		status, err := r.request(ctx, http.MethodPost, baseURL+"users", bootstrapKey, body, &issued)
		if err != nil || status != http.StatusCreated {
			return RegistrationResult{Status: "user-registration"}, err
		}
		created = "user"
	}

	if len(issued.APIKey) < 24 || issued.DeviceID == "" || issued.UserID == "" {
		return RegistrationResult{Status: "registration-response"}, errors.New("registration-response-incomplete")
	}
	identity, err := r.validateKey(ctx, baseURL, issued.APIKey)
	if err != nil || identity.UserID != issued.UserID || identity.DeviceID != issued.DeviceID {
		return RegistrationResult{Status: "issued-key-invalid"}, errors.New("issued-credential-validation-failed")
	}
	cutover := r.Clock().UTC()
	configuration := Config{Enabled: true, BaseURL: baseURL, APIKey: issued.APIKey, CutoverAt: cutover, SendLocation: true}
	if err = writePrivateJSON(r.Options.ConfigPath, configuration, 0o640); err != nil {
		return RegistrationResult{Status: "config-write"}, err
	}
	if issued.RecoveryCode != "" && r.Options.RecoveryPath != "" {
		recovery := map[string]string{"userId": issued.UserID, "userHandle": issued.UserHandle, "recoveryCode": issued.RecoveryCode, "createdAt": cutover.Format(time.RFC3339)}
		if err = writePrivateJSON(r.Options.RecoveryPath, recovery, 0o600); err != nil {
			_ = os.Remove(r.Options.ConfigPath)
			return RegistrationResult{Status: "recovery-write"}, err
		}
	}
	return RegistrationResult{Configured: true, Authenticated: true, Created: created, Status: "ok"}, nil
}

func EnsurePrivateParent(path string) error {
	return os.MkdirAll(filepath.Dir(path), 0o750)
}
