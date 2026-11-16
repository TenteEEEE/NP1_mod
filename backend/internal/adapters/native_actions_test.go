package adapters

import (
	"encoding/hex"
	"encoding/json"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestActionCatalogueMatchesFixedHelperAllowlist(t *testing.T) {
	data, e := os.ReadFile("../../../tools/native/operations-actions.h")
	if e != nil {
		t.Fatal(e)
	}
	seen := map[string]bool{}
	for _, a := range nativeActionCatalog() {
		if seen[a.ID] {
			t.Fatal("duplicate", a.ID)
		}
		seen[a.ID] = true
		if a.UnavailableReason == "" && !strings.Contains(string(data), `{"`+a.ID+`","`+a.Owner+`",`) {
			t.Fatal("helper binding missing", a.ID)
		}
		if !a.ReadOnly && a.Confirmation == "" {
			t.Fatal("confirmation missing", a.ID)
		}
	}
	if a, _ := nativeActionSpec("sensor-trip"); a.UnavailableReason == "" {
		t.Fatal("unsupported FW7 request exposed")
	}
}
func TestTypedActionInputsRejectUnknownFieldsRangesAndCredentialInjection(t *testing.T) {
	for _, tc := range []struct {
		id, body string
		valid    bool
	}{
		{"navigation-start", `{"route":4294967295}`, true}, {"navigation-start", `{"route":4294967296}`, false}, {"navigation-start", `{"route":1.5}`, false}, {"navigation-start", `{"route":1,"opcode":5}`, false},
		{"account-login", `{"refreshToken":"synthetic","userKey":"fixture"}`, true}, {"account-login", `{"refreshToken":"synthetic\nsecret","userKey":"fixture"}`, false},
		{"wifi-connect", `{"ssid":"fixture","security":1,"password":""}`, true}, {"wifi-connect", `{"ssid":"fixture","security":2,"password":"short"}`, false}, {"wifi-connect", `{"ssid":"fixture","security":2,"password":"synthetic-only"}`, true}, {"wifi-delete", `{"ssid":"fixture","security":4}`, false},
		{"license-export", `{"path":"/tmp"}`, false}, {"serial-input", `{"value":2}`, false}, {"alignment-camera", `{"camera":2}`, true},
	} {
		a, _ := nativeActionSpec(tc.id)
		values := map[string]any{}
		json.Unmarshal([]byte(tc.body), &values)
		_, e := actionPayload(a, values)
		if (e == nil) != tc.valid {
			t.Fatal(tc.id, tc.body, e)
		}
	}
}
func TestActionResponseRejectsUncorrelatedCleanupMalformedAndFalseSuccess(t *testing.T) {
	a, _ := nativeActionSpec("account-login")
	for _, bad := range []string{"payload=0801", "payload=0801\ncleanup=matched; own-receiver-removed\nextra", "payload=08010800\ncleanup=matched; own-receiver-removed", "payload=0802\ncleanup=matched; own-receiver-removed", "payload=10\ncleanup=matched; own-receiver-removed"} {
		if _, e := parseActionOutput(a, []byte(bad)); e == nil {
			t.Fatal("invalid output accepted", bad)
		}
	}
	reply, e := parseActionOutput(a, []byte("payload=0801\ncleanup=matched; own-receiver-removed\n"))
	if e != nil || reply.Values["result"] != true || !reply.Acknowledged {
		t.Fatal(reply, e)
	}
	a, _ = nativeActionSpec("factory-reset")
	reply, e = parseActionOutput(a, []byte("sent=1\ncleanup=matched; own-receiver-removed\n"))
	if e != nil || !reply.Sent || reply.Acknowledged {
		t.Fatal("notification misreported", reply, e)
	}
	a, _ = nativeActionSpec("wifi-nearby")
	wire, _ := hex.DecodeString("0801180120012a0d0a076669787475726510021803")
	v, e := decodeActionPayload(a, wire)
	if e != nil || len(v["networks"].([]map[string]any)) != 1 {
		t.Fatal(v, e)
	}
	for _, bad := range []string{"28012a00", "2a05ff", "2a0008010801"} {
		wire, _ := hex.DecodeString(bad)
		if _, e = decodeActionPayload(a, wire); e == nil {
			t.Fatal("bad nested data accepted", bad)
		}
	}
}
func TestActionHandlerConfirmationModeReadbackAndNoRetry(t *testing.T) {
	for _, tc := range []struct {
		id, body       string
		status, writes int
	}{
		{"rear-window-reset", `{"values":{}}`, 400, 0},
		{"rear-window-reset", `{"confirmation":"リアウインドウの学習をリセット","values":{}}`, 409, 1},
		{"factory-reset", `{"confirmation":"本体を初期化","values":{}}`, 200, 1},
		{"account-login", `{"confirmation":"本体にログイン","values":{"refreshToken":"synthetic","userKey":"fixture"},"raw":true}`, 400, 0},
		{"sensor-trip-reset", `{}`, 409, 0},
		{"serial-input", `{"confirmation":"シリアルのコンソール入力を変更","values":{"value":1}}`, 400, 0},
		{"serial-input", `{"confirmation":"シリアルのコンソール入力を変更","values":{"value":1},"expectedValue":false}`, 503, 1},
	} {
		t.Run(tc.id+tc.body, func(t *testing.T) {
			writes := 0
			controller := nativeOperationsController{now: time.Now, action: func(a NativeAction, payload []byte) (actionReply, error) {
				switch a.ID {
				case "maintenance-status":
					return actionReply{Values: map[string]any{"mode": uint64(2)}}, nil
				case "serial-status":
					return actionReply{Values: map[string]any{"result": true, "input": false, "output": false}}, nil
				}
				writes++
				switch a.ID {
				case "factory-reset":
					return actionReply{Sent: true, Values: map[string]any{}}, nil
				case "rear-window-reset":
					return actionReply{Sent: true, Acknowledged: true, Values: map[string]any{"result": uint64(1)}}, nil
				}
				return actionReply{Sent: true, Acknowledged: true, Values: map[string]any{"result": true}}, nil
			}}
			w := httptest.NewRecorder()
			controller.handle(w, httptest.NewRequest("POST", "/operations/actions/"+tc.id, strings.NewReader(tc.body)))
			if w.Code != tc.status || writes != tc.writes {
				t.Fatal(w.Code, w.Body.String(), writes)
			}
			if tc.id == "factory-reset" && strings.Contains(w.Body.String(), `"completionConfirmed":true`) {
				t.Fatal("reset notification treated as completion")
			}
		})
	}
}

func TestNearbyWiFiReadRequiresStationOnWithoutChangingIt(t *testing.T) {
	calls := []string{}
	c := nativeOperationsController{now: time.Now, action: func(a NativeAction, _ []byte) (actionReply, error) {
		calls = append(calls, a.ID)
		return actionReply{Values: map[string]any{"result": uint64(1), "setting": uint64(2)}}, nil
	}}
	w := httptest.NewRecorder()
	c.handle(w, httptest.NewRequest("POST", "/operations/actions/wifi-nearby", strings.NewReader(`{"values":{"page":1}}`)))
	if w.Code != 409 || strings.Join(calls, ",") != "wifi-status" {
		t.Fatal(w.Code, calls)
	}
}
