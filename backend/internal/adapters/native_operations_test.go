package adapters

import (
	"encoding/json"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNativeOperationsFormatRequiresFreshCardBoundSingleUseConfirmation(t *testing.T) {
	for _, condition := range []string{"missing-confirmation", "expired", "swapped-card", "duplicate", "accepted-not-completed", "native-failure"} {
		t.Run(condition, func(t *testing.T) {
			now := time.Date(2026, 11, 16, 0, 0, 0, 0, time.UTC)
			card := formatCard{"card-A", 64 << 30}
			formats := 0
			controller := nativeOperationsController{now: func() time.Time { return now }, card: func() (formatCard, error) { return card, nil }, tickets: map[string]formatTicket{}, run: func(group string, args ...string) (map[string]uint32, error) {
				if args[0] == "--format" {
					formats++
					if len(args) != 3 || args[2] != "card-A" {
						t.Fatal("card identity not forwarded")
					}
					if condition == "native-failure" {
						return nil, errors.New("timeout")
					}
					return map[string]uint32{"accepted": 1, "formatStatus": 1}, nil
				}
				return map[string]uint32{"inserted": 1, "formatStatus": 2}, nil
			}}
			call := func(path, body string) *httptest.ResponseRecorder {
				w := httptest.NewRecorder()
				controller.handle(w, httptest.NewRequest("POST", "/operations/sd-format/"+path, strings.NewReader(body)))
				return w
			}
			prepared := call("prepare", "{}")
			if prepared.Code != 200 {
				t.Fatal(prepared.Body.String())
			}
			var preview struct {
				Token string `json:"token"`
			}
			json.Unmarshal(prepared.Body.Bytes(), &preview)
			if len(preview.Token) != 64 {
				t.Fatal("confirmation nonce missing")
			}
			if condition == "expired" {
				now = now.Add(61 * time.Second)
			}
			if condition == "swapped-card" {
				card.Fingerprint = "card-B"
			}
			confirm := "SDカードを消去"
			if condition == "missing-confirmation" {
				confirm = ""
			}
			body, _ := json.Marshal(map[string]any{"token": preview.Token, "confirmation": confirm, "acknowledgeDataLoss": true})
			response := call("execute", string(body))
			switch condition {
			case "expired", "swapped-card", "missing-confirmation":
				if formats != 0 || response.Code < 400 {
					t.Fatal("format escaped confirmation", response.Code, formats)
				}
			case "native-failure":
				if formats != 1 || response.Code != 503 {
					t.Fatal("failure was retried or reported success")
				}
			default:
				if formats != 1 || response.Code != 202 || !strings.Contains(response.Body.String(), `"completionConfirmed":false`) {
					t.Fatal("acceptance confused with completion", response.Body.String())
				}
				duplicate := call("execute", string(body))
				if duplicate.Code != 409 || formats != 1 {
					t.Fatal("format ticket replayed")
				}
			}
		})
	}
}

func TestNativeOperationsRejectInvalidOrUnknownRequestsBeforeCallingOwner(t *testing.T) {
	controller := nativeOperationsController{run: func(string, ...string) (map[string]uint32, error) {
		t.Fatal("unexpected native operation")
		return nil, nil
	}}
	for _, request := range []struct{ method, path, body string }{
		{"GET", "state/unknown", ""}, {"PUT", "settings/unknown", `{"value":1,"expectedValue":1}`},
		{"PUT", "settings/recorder-front", `{"value":3,"expectedValue":1}`}, {"PUT", "settings/recorder-front", `{"value":1}`},
		{"PUT", "settings/recorder-front", `{"value":1,"expectedValue":true}`}, {"PUT", "settings/recorder-front", `{"value":1,"expectedValue":1,"command":"format"}`},
		{"POST", "sd-format/prepare", `{} {}`}, {"POST", "sd-format/execute", `{"confirmation":"SDカードを消去"}`},
	} {
		w := httptest.NewRecorder()
		controller.handle(w, httptest.NewRequest(request.method, "/operations/"+request.path, strings.NewReader(request.body)))
		if w.Code < 400 {
			t.Fatal("invalid operation accepted", request)
		}
	}
}

func TestNativeOperationsRequiresMatchingSettingReadback(t *testing.T) {
	for _, match := range []bool{false, true} {
		controller := nativeOperationsController{now: time.Now, run: func(group string, args ...string) (map[string]uint32, error) {
			if group != "recorder" || strings.Join(args, " ") != "--set recorder-front 1 2" {
				t.Fatal("unexpected request", group, args)
			}
			value := uint32(1)
			if match {
				value = 2
			}
			return map[string]uint32{"field1": value}, nil
		}}
		w := httptest.NewRecorder()
		controller.handle(w, httptest.NewRequest("PUT", "/operations/settings/recorder-front", strings.NewReader(`{"expectedValue":1,"value":2}`)))
		if match && w.Code != 200 || !match && w.Code != 503 {
			t.Fatal("readback mismatch", match, w.Code)
		}
	}
}

func TestSDFormatIdentityExcludesInternalMMCAndDetectsCardChanges(t *testing.T) {
	root := t.TempDir()
	write := func(disk, kind, cid string) {
		dir := filepath.Join(root, disk)
		os.MkdirAll(filepath.Join(dir, "device"), 0700)
		os.WriteFile(filepath.Join(dir, "device/type"), []byte(kind+"\n"), 0600)
		os.WriteFile(filepath.Join(dir, "device/cid"), []byte(cid+"\n"), 0600)
		os.WriteFile(filepath.Join(dir, "size"), []byte("67108864\n"), 0600)
	}
	write("mmcblk0", "MMC", strings.Repeat("0", 32))
	write("mmcblk1", "SD", strings.Repeat("1", 32))
	before, e := sdFormatCardAt(root)
	if e != nil || before.CapacityBytes != 32<<30 {
		t.Fatal("card missing", e, before)
	}
	write("mmcblk1", "SD", strings.Repeat("2", 32))
	after, e := sdFormatCardAt(root)
	if e != nil || after.Fingerprint == before.Fingerprint {
		t.Fatal("replacement not detected")
	}
	write("mmcblk2", "SD", strings.Repeat("3", 32))
	if _, e = sdFormatCardAt(root); e == nil {
		t.Fatal("ambiguous cards accepted")
	}
}

func TestOperationsOutputRequiresCleanupAndRejectsDuplicates(t *testing.T) {
	for _, invalid := range []string{"field1=1", "field1=1\nfield1=1\ncleanup=matched; own-receiver-removed", "field1=-1\ncleanup=matched; own-receiver-removed", "field1=01\ncleanup=matched; own-receiver-removed"} {
		if _, e := parseOperationsOutput([]byte(invalid)); e == nil {
			t.Fatal("invalid output accepted")
		}
	}
	if value, e := parseOperationsOutput([]byte("field1=1\ncleanup=matched; own-receiver-removed\n")); e != nil || value["field1"] != 1 {
		t.Fatal("valid output lost", e)
	}
}

func TestAccountStateCatalogDistinguishesLabelFromCurrentValue(t *testing.T) {
	for _, group := range nativeOperationGroups() {
		if group.ID != "account" {
			continue
		}
		if group.Label != "アカウント" || len(group.Items) != 1 {
			t.Fatalf("ambiguous account group: %+v", group)
		}
		item := group.Items[0]
		if item.ID != "login-state" || item.Label != "ログイン状態" || item.Writable || len(item.Options) != 3 {
			t.Fatalf("ambiguous login state item: %+v", item)
		}
		if item.Options[0].Label != "ログイン済み" || item.Options[1].Label != "未ログイン" || item.Options[2].Label != "起動時の認証更新中" {
			t.Fatalf("login states changed: %+v", item.Options)
		}
		return
	}
	t.Fatal("account group missing")
}
