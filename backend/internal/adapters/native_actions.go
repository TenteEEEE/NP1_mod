package adapters

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"os/exec"
	"strings"
	"unicode/utf8"
)

// This catalogue is also the public schema. No raw packet/owner/opcode API is exposed.
type NativeActionInput struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Kind  string `json:"kind"`
	Min   uint32 `json:"min,omitempty"`
	Max   uint32 `json:"max,omitempty"`
	Field uint32 `json:"-"`
}
type NativeAction struct {
	ID                string              `json:"id"`
	Group             string              `json:"group"`
	Label             string              `json:"label"`
	Description       string              `json:"description"`
	ReadOnly          bool                `json:"readOnly"`
	Confirmation      string              `json:"confirmation,omitempty"`
	Inputs            []NativeActionInput `json:"inputs"`
	Owner             string              `json:"-"`
	Response          []actionField       `json:"-"`
	Result            uint32              `json:"-"`
	Success           uint64              `json:"-"`
	Notify            bool                `json:"-"`
	UnavailableReason string              `json:"unavailableReason,omitempty"`
}
type actionField struct {
	Number     uint32
	Name, Kind string
}

func actionNumber(id, label string, field, min, max uint32) NativeActionInput {
	return NativeActionInput{id, label, "number", min, max, field}
}
func nativeActionCatalog() []NativeAction {
	empty := []NativeActionInput{}
	boolResult := []actionField{{1, "result", "bool"}}
	route := []NativeActionInput{actionNumber("route", "同期済みのルート番号", 1, 0, 0xffffffff)}
	credentials := []NativeActionInput{{"refreshToken", "リフレッシュトークン", "secret", 0, 1024, 2}, {"userKey", "ユーザーキー", "secret", 0, 256, 3}}
	checkin := append(append([]NativeActionInput{}, credentials...), NativeActionInput{"lastName", "姓", "text", 0, 128, 4}, NativeActionInput{"firstName", "名", "text", 0, 128, 5})
	makeAction := func(id, group, label, description, owner string, ro bool, input []NativeActionInput, response []actionField) NativeAction {
		a := NativeAction{ID: id, Group: group, Label: label, Description: description, Owner: owner, ReadOnly: ro, Inputs: input, Response: response}
		if !ro {
			a.Confirmation = label
		}
		return a
	}
	list := []NativeAction{
		makeAction("alignment-start", "取付の調整", "取付調整を開始", "停車中に映像を見ながら調整します。開始後はこの画面を開いたままにしてください。", "DriveRec", false, empty, boolResult),
		makeAction("alignment-cancel", "取付の調整", "取付調整を中止", "調整モードの中止通知を送ります。", "DriveRec", false, empty, nil),
		makeAction("alignment-finish", "取付の調整", "取付調整を完了", "実際の取付と角度を確認してから完了します。", "DriveRec", false, empty, nil),
		makeAction("alignment-keepalive", "取付の調整", "調整モードの接続確認", "調整中の接続を維持します。", "DriveRec", false, empty, boolResult),
		makeAction("alignment-camera", "取付の調整", "調整用カメラを切替", "1：前方、2：車内。ライブ画面で映像を確認してください。", "DriveRec", false, []NativeActionInput{actionNumber("camera", "カメラ", 1, 1, 2)}, boolResult),
		makeAction("alignment-ap", "取付の調整", "調整用Wi-Fi情報を取得", "SSIDとパスワードをこの画面だけに表示します。", "DriveRec", true, empty, []actionField{{1, "ssid", "string"}, {2, "password", "string"}, {3, "security", "number"}, {4, "band", "number"}}),
		makeAction("rear-window-status", "取付の調整", "リアウインドウの学習状態", "1：未学習、2：学習中、3：学習済み。", "ImageRecog", true, empty, []actionField{{1, "learningState", "number"}}),
		makeAction("rear-window-reset", "取付の調整", "リアウインドウの学習をリセット", "学習済みの状態を消去し、学習し直します。", "ImageRecog", false, empty, []actionField{{1, "result", "number"}}),
		makeAction("gnss-info", "GNSS・センサー", "GNSSの受信状態を取得", "アンテナ・測位状態・受信衛星数・使用衛星数を取得します。", "SitAwrEng", true, empty, []actionField{{1, "antennaConnected", "bool"}, {2, "antennaLevel", "number"}, {3, "positioningStatus", "number"}, {4, "satellites", "number"}, {5, "usedSatellites", "number"}, {6, "mounted", "bool"}}),
		makeAction("sensor-trip", "GNSS・センサー", "センサートリップを取得", "純正センサーのトリップ値を読み取ります。", "SitAwrEng", true, empty, []actionField{{1, "result", "bool"}, {2, "trip", "number"}}),
		makeAction("sensor-clear", "GNSS・センサー", "センサー学習をクリア", "純正センサーの学習をクリアします。再学習が必要になります。", "SitAwrEng", false, empty, boolResult),
		makeAction("sensor-trip-reset", "GNSS・センサー", "センサートリップをリセット", "純正センサーのトリップ値をリセットします。NCCの走行履歴は消去しません。", "SitAwrEng", false, empty, boolResult),
		makeAction("license-export", "ライセンス・診断", "OSSライセンスをSDへ保存", "本体のOSSLicenseData/OSSLicenseData.pdfをSDへ転送します。約20秒お待ちください。", "System", false, empty, boolResult),
		makeAction("diagnostic-export", "ライセンス・診断", "診断ログをSDへ保存", "Poseidon保守モードとSDが必要です。位置などを含む診断ログをSDへ保存します。", "System", false, empty, boolResult),
		makeAction("maintenance-status", "保守・シリアル", "保守モードを取得", "0：不明、1：OFF、2：Poseidon、3：APP、4：Sales、5：Service。", "System", true, empty, []actionField{{1, "mode", "number"}}),
		makeAction("maintenance-start", "保守・シリアル", "保守モードのキー入力を開始", "60秒以内に本体の音量キーで + − + − − + を入力し、パスワードを送信してください。", "System", false, empty, nil),
		makeAction("maintenance-verify", "保守・シリアル", "保守パスワードを送信", "本体のキー入力を完了してから送信します。パスワードは保存しません。", "System", false, []NativeActionInput{{"password", "保守パスワード", "secret", 0, 256, 1}}, []actionField{{1, "mode", "number"}}),
		makeAction("maintenance-cancel", "保守・シリアル", "保守モードの入力を終了", "入退場のキー入力フローを終了します。現在のモードは別途再取得します。", "System", false, empty, nil),
		makeAction("serial-status", "保守・シリアル", "シリアルの状態を取得", "Poseidon保守モードでログ出力とコンソール入力の状態を取得します。", "System", true, empty, []actionField{{1, "result", "bool"}, {2, "output", "bool"}, {3, "input", "bool"}}),
		makeAction("serial-output", "保守・シリアル", "シリアルのログ出力を変更", "1：ON、0：OFF。変更後の実値を再取得します。", "System", false, []NativeActionInput{actionNumber("value", "出力", 1, 0, 1)}, boolResult),
		makeAction("serial-input", "保守・シリアル", "シリアルのコンソール入力を変更", "1：ON、0：OFF。ONでは物理UARTから操作できます。使用後はOFFへ戻してください。", "System", false, []NativeActionInput{actionNumber("value", "入力", 1, 0, 1)}, boolResult),
		makeAction("navigation-start", "本体のナビ操作", "同期済みルートの案内を開始", "My NP1で同期済みのルート番号が必要です。ここでは目的地検索やルート生成は行いません。", "Navigation", false, route, []actionField{{1, "routeSynchronized", "bool"}}),
		makeAction("navigation-update", "本体のナビ操作", "同期済みルートへ更新", "同期済みのルート番号を本体へ指定します。", "Navigation", false, route, []actionField{{1, "routeSynchronized", "bool"}}),
		makeAction("navigation-stop", "本体のナビ操作", "ナビの案内を終了", "本体の現在の案内を終了します。", "Navigation", false, empty, nil),
		makeAction("navigation-road", "本体のナビ操作", "ルートの道路を切替", "同期済みのルート番号を本体へ指定します。", "Navigation", false, route, []actionField{{1, "routeSynchronized", "bool"}}),
		makeAction("account-login", "本体アカウントの操作", "本体にログイン", "My Pioneerの有効なリフレッシュトークンとユーザーキーが必要です。値は保存しません。", "Setting", false, credentials, boolResult),
		makeAction("account-checkin", "本体アカウントの操作", "ドライバーをチェックイン", "有効な認証情報と氏名が必要です。値は保存しません。", "Setting", false, checkin, boolResult),
		makeAction("owner-key", "本体アカウントの操作", "オーナーのユーザーキーを取得", "機密情報をこの画面だけに表示します。第三者に共有しないでください。", "Setting", false, empty, []actionField{{1, "result", "bool"}, {2, "ownerKey", "string"}}),
		makeAction("vehicle-setup-status", "セットアップ", "車両設定の完了状態を取得", "1：設定済み、2：未設定。", "Setting", true, empty, []actionField{{1, "status", "number"}}),
		makeAction("vehicle-setup-finish", "セットアップ", "車両設定を完了", "実際の車種・取付を確認してから完了通知を送ります。", "Setting", false, empty, nil),
		makeAction("initial-setup-finish", "セットアップ", "初期設定の完了状態を通知", "1：完了、2：通信設定以外は完了、3：未完了。実際の設定と一致する状態を指定します。", "Setting", false, []NativeActionInput{actionNumber("status", "完了状態", 1, 1, 3)}, nil),
	}
	list = append(list, nativeNetworkActions()...)
	for _, entry := range []struct{ id, label string }{{"firmware-check", "純正ファームウェアを確認"}, {"firmware-download", "純正ファームウェアをダウンロード"}, {"firmware-cancel", "純正ファームウェアの取得を中止"}, {"firmware-activate", "純正ファームウェアを適用"}} {
		a := makeAction(entry.id, "純正ファームウェア", entry.label, "純正更新はNP1modの動作や復旧手段に影響します。", "Update", false, empty, nil)
		a.UnavailableReason = "この環境では純正rootfs・サービスの変更を禁止しているため実行できません。更新の確認だけでも自動適用へ進む要求は送信しません。"
		list = append(list, a)
	}
	list = append(list, makeAction("factory-reset", "セットアップ", "本体を初期化", "純正のユーザー設定・認証情報を初期化します。NP1modの接続や設定も失われる可能性があります。バックアップと復旧手段を準備してください。", "Setting", false, empty, nil))
	for i := range list {
		a := &list[i]
		switch a.ID {
		case "alignment-cancel", "alignment-finish", "maintenance-start", "maintenance-cancel", "vehicle-setup-finish", "initial-setup-finish", "factory-reset":
			a.Notify = true
		}
		if a.ID == "sensor-trip" || a.ID == "sensor-trip-reset" {
			a.UnavailableReason = "FW7の所有プロセスはこの要求を処理しません（要求形式はAPKで確認済み）。"
		}
		if len(a.Response) > 0 && a.Response[0].Name == "result" {
			a.Result = 1
			a.Success = 1
			if a.ID == "rear-window-reset" {
				a.Success = 2
			}
		}
		if strings.HasPrefix(a.ID, "navigation-") && a.ID != "navigation-stop" {
			a.Result = 1
			a.Success = 1
		}
	}
	return list
}
func nativeActionSpec(id string) (NativeAction, bool) {
	for _, a := range nativeActionCatalog() {
		if a.ID == id {
			return a, true
		}
	}
	return NativeAction{}, false
}

func actionPayload(a NativeAction, values map[string]any) ([]byte, error) {
	if len(values) != len(a.Inputs) {
		return nil, errors.New("unexpected input count")
	}
	var out []byte
	for _, f := range a.Inputs {
		v, ok := values[f.ID]
		if !ok {
			return nil, errors.New("missing input")
		}
		if f.Kind == "number" {
			n, ok := v.(float64)
			if !ok || n < float64(f.Min) || n > float64(f.Max) || n != float64(uint32(n)) {
				return nil, errors.New("invalid number")
			}
			out = binary.AppendUvarint(out, uint64(f.Field<<3))
			out = binary.AppendUvarint(out, uint64(n))
		} else {
			s, ok := v.(string)
			if !ok || len(s) == 0 && f.Kind != "optional-secret" || len(s) > int(f.Max) || !utf8.ValidString(s) || strings.ContainsAny(s, "\x00\r\n") {
				return nil, errors.New("invalid string")
			}
			if f.Kind == "ascii" || f.Kind == "optional-secret" {
				for _, c := range []byte(s) {
					if c < 32 || c > 126 {
						return nil, errors.New("invalid network text")
					}
				}
			}
			out = binary.AppendUvarint(out, uint64(f.Field<<3|2))
			out = binary.AppendUvarint(out, uint64(len(s)))
			out = append(out, []byte(s)...)
		}
	}
	if a.ID == "wifi-connect" || a.ID == "local-ap-settings" {
		password, _ := values["password"].(string)
		security, _ := values["security"].(float64)
		if security != 1 && len(password) < 8 || security == 1 && password != "" {
			return nil, errors.New("invalid network password")
		}
	}
	if len(out) > 2048 {
		return nil, errors.New("oversized input")
	}
	return out, nil
}
func decodeActionPayload(a NativeAction, data []byte) (map[string]any, error) {
	if len(data) > 4096 {
		return nil, errors.New("oversized response")
	}
	out := map[string]any{}
	spec := map[uint32]actionField{}
	for _, f := range a.Response {
		spec[f.Number] = f
		switch f.Kind {
		case "bool":
			out[f.Name] = false
		case "string":
			out[f.Name] = ""
		case "saved-ap-list", "nearby-ap-list":
			out[f.Name] = []map[string]any{}
		default:
			out[f.Name] = uint64(0)
		}
	}
	seen := map[uint32]bool{}
	for len(data) > 0 {
		key, n := binary.Uvarint(data)
		if n <= 0 {
			return nil, errors.New("invalid response key")
		}
		data = data[n:]
		field := uint32(key >> 3)
		f, ok := spec[field]
		if !ok || seen[field] && !strings.HasSuffix(f.Kind, "-ap-list") || key>>3 != uint64(field) {
			return nil, errors.New("unexpected response field")
		}
		seen[field] = true
		if key&7 == 0 && (f.Kind == "number" || f.Kind == "bool") {
			v, n := binary.Uvarint(data)
			if n <= 0 || f.Kind == "bool" && v > 1 {
				return nil, errors.New("invalid response value")
			}
			data = data[n:]
			if f.Kind == "bool" {
				out[f.Name] = v == 1
			} else {
				out[f.Name] = v
			}
		} else if key&7 == 2 && strings.HasSuffix(f.Kind, "-ap-list") {
			v, n := binary.Uvarint(data)
			if n <= 0 || v > 512 || v > uint64(len(data)-n) {
				return nil, errors.New("invalid nested response")
			}
			schema := []actionField{{1, "ssid", "string"}, {2, "security", "number"}, {3, "strength", "number"}}
			if f.Kind == "saved-ap-list" {
				schema = []actionField{{1, "ssid", "string"}, {2, "autoConnect", "number"}, {3, "security", "number"}, {4, "strength", "number"}, {5, "connected", "number"}}
			}
			item, e := decodeActionPayload(NativeAction{Response: schema}, data[n:n+int(v)])
			if e != nil {
				return nil, e
			}
			list := out[f.Name].([]map[string]any)
			if len(list) >= 5 {
				return nil, errors.New("oversized network list")
			}
			out[f.Name] = append(list, item)
			data = data[n+int(v):]
		} else if key&7 == 2 && f.Kind == "string" {
			v, n := binary.Uvarint(data)
			if n <= 0 || v > 2048 || v > uint64(len(data)-n) {
				return nil, errors.New("invalid response length")
			}
			s := data[n : n+int(v)]
			if !utf8.Valid(s) || bytes.IndexByte(s, 0) >= 0 {
				return nil, errors.New("invalid response text")
			}
			out[f.Name] = string(s)
			data = data[n+int(v):]
		} else {
			return nil, errors.New("invalid response type")
		}
	}
	return out, nil
}

type actionReply struct {
	Values       map[string]any `json:"values"`
	Sent         bool           `json:"sent"`
	Acknowledged bool           `json:"acknowledged"`
}
type actionRunner func(NativeAction, []byte) (actionReply, error)

func runNativeAction(a NativeAction, payload []byte) (actionReply, error) {
	if !nativeFirmwareMatches(operationsHelper, operationsHelperHash) {
		return actionReply{}, errors.New("helper unavailable")
	}
	_, before, e := operationsOwner(a.ID)
	if e != nil {
		return actionReply{}, e
	}
	cmd := exec.Command(operationsHelper, "--action", before.pid, a.ID)
	cmd.Env = []string{"PATH=/usr/bin:/bin", "LD_LIBRARY_PATH=/usr/local/lib", "NCC_NATIVE_OPERATIONS=1"}
	cmd.Stdin = bytes.NewReader(payload)
	// Credentials never enter argv, disk logs or the audit trail. Stdout is bounded
	// by the fixed helper; stderr contains only fixed failure messages.
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = io.Discard
	if e = cmd.Run(); e != nil {
		return actionReply{}, e
	}
	_, after, e := operationsOwner(a.ID)
	if e != nil || after != before {
		return actionReply{}, errors.New("owner changed")
	}
	return parseActionOutput(a, stdout.Bytes())
}
func parseActionOutput(a NativeAction, data []byte) (actionReply, error) {
	if len(data) > 8300 {
		return actionReply{}, errors.New("oversized output")
	}
	lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	if len(lines) != 2 || lines[1] != "cleanup=matched; own-receiver-removed" {
		return actionReply{}, errors.New("cleanup unconfirmed")
	}
	if a.Notify {
		if lines[0] != "sent=1" {
			return actionReply{}, errors.New("send unconfirmed")
		}
		return actionReply{Sent: true, Values: map[string]any{}}, nil
	}
	text, ok := strings.CutPrefix(lines[0], "payload=")
	if !ok {
		return actionReply{}, errors.New("payload missing")
	}
	wire, e := hex.DecodeString(text)
	if e != nil {
		return actionReply{}, e
	}
	values, e := decodeActionPayload(a, wire)
	return actionReply{values, true, e == nil}, e
}
func (c *nativeOperationsController) handleAction(w http.ResponseWriter, r *http.Request, id string) {
	a, ok := nativeActionSpec(id)
	if !ok {
		nativeError(w, 404, "UNKNOWN_OPERATION", "対応していない操作です")
		return
	}
	if a.UnavailableReason != "" {
		nativeError(w, 409, "UNSUPPORTED_FIRMWARE_OPERATION", a.UnavailableReason)
		return
	}
	readGet := a.ReadOnly && len(a.Inputs) == 0
	if (readGet && r.Method != "GET") || (!readGet && r.Method != "POST") {
		nativeError(w, 405, "METHOD_NOT_ALLOWED", "操作方法を確認してください")
		return
	}
	body := struct {
		Confirmation  string         `json:"confirmation"`
		Values        map[string]any `json:"values"`
		ExpectedValue *bool          `json:"expectedValue,omitempty"`
	}{}
	if !readGet && !decodeOperation(w, r, &body) {
		return
	}
	if !a.ReadOnly && body.Confirmation != a.Confirmation {
		nativeError(w, 400, "CONFIRMATION_REQUIRED", "操作内容を確認してから実行してください")
		return
	}
	payload, e := actionPayload(a, body.Values)
	if e != nil {
		nativeError(w, 400, "INVALID_OPERATION", "入力内容を確認してください")
		return
	}
	run := c.action
	if run == nil {
		run = runNativeAction
	}
	if id == "wifi-connect" || id == "wifi-connect-saved" || id == "wifi-nearby" {
		spec, _ := nativeActionSpec("wifi-status")
		state, e := run(spec, nil)
		if e != nil || state.Values["result"] != uint64(1) || state.Values["setting"] != uint64(1) {
			nativeError(w, 409, "WIFI_NOT_READY", "先に純正Wi-FiをONにしてください")
			return
		}
	}
	fail := func() {
		nativeError(w, 503, "OPERATION_UNCONFIRMED", "本体の応答を確認できません。変更を自動で再送しません。状態を再取得してください。")
	}
	if strings.HasPrefix(id, "serial-") || id == "diagnostic-export" {
		mode, _ := nativeActionSpec("maintenance-status")
		state, e := run(mode, nil)
		if e != nil || state.Values["mode"] != uint64(2) {
			nativeError(w, 409, "MAINTENANCE_REQUIRED", "Poseidon保守モードを確認できません")
			return
		}
	}
	if id == "license-export" || id == "diagnostic-export" {
		if _, e := c.card(); e != nil || SDStatus().Status != "available" {
			nativeError(w, 409, "SD_NOT_READY", "SDが挿入され、マウントされている必要があります")
			return
		}
	}
	if id == "serial-output" || id == "serial-input" {
		if body.ExpectedValue == nil {
			nativeError(w, 400, "EXPECTED_VALUE_REQUIRED", "変更前の状態が必要です")
			return
		}
		spec, _ := nativeActionSpec("serial-status")
		before, e := run(spec, nil)
		key := strings.TrimPrefix(id, "serial-")
		if e != nil || before.Values["result"] != true || before.Values[key] != *body.ExpectedValue {
			nativeError(w, 409, "STATE_CHANGED", "シリアルの状態が変わりました。再取得してください")
			return
		}
	}
	reply, e := run(a, payload)
	if e != nil {
		fail()
		return
	}
	if a.Result != 0 {
		v := reply.Values[a.Response[0].Name]
		success := v == a.Success
		if boolean, ok := v.(bool); ok {
			success = boolean && a.Success == 1
		}
		if !success {
			nativeError(w, 409, "OPERATION_REJECTED", "本体が操作を受け付けませんでした")
			return
		}
	}
	result := map[string]any{"values": reply.Values, "sent": reply.Sent, "acknowledged": reply.Acknowledged, "completionConfirmed": false, "observedAt": c.now().UTC(), "message": "本体の応答を確認しました。"}
	if a.Notify {
		result["message"] = "通知を送信しました。処理の完了はまだ確認していません。"
	}
	followup := ""
	switch id {
	case "maintenance-verify", "maintenance-cancel":
		followup = "maintenance-status"
	case "serial-output", "serial-input":
		followup = "serial-status"
	case "rear-window-reset":
		followup = "rear-window-status"
	case "sensor-trip-reset":
		followup = "sensor-trip"
	case "vehicle-setup-finish":
		followup = "vehicle-setup-status"
	}
	if followup != "" {
		spec, _ := nativeActionSpec(followup)
		state, e := run(spec, nil)
		if e != nil {
			fail()
			return
		}
		result["readback"] = state.Values
		if strings.HasPrefix(id, "serial-") {
			key := strings.TrimPrefix(id, "serial-")
			if state.Values["result"] != true || state.Values[key] != (body.Values["value"].(float64) == 1) {
				fail()
				return
			}
			result["completionConfirmed"] = true
		}
	}
	if id == "wifi-switch" || id == "local-ap-switch" {
		if reply.Values["setting"] != uint64(body.Values["setting"].(float64)) {
			fail()
			return
		}
	}
	if id == "wifi-autoconnect" || id == "wifi-delete" || id == "wifi-reset" || id == "wifi-disconnect" || id == "wifi-connect" || id == "wifi-connect-saved" {
		spec, _ := nativeActionSpec("wifi-status")
		state, e := run(spec, nil)
		if e == nil {
			result["readback"] = state.Values
		}
	}
	if a.ReadOnly {
		result["completionConfirmed"] = true
	}
	recordingJSON(w, 200, result)
}
