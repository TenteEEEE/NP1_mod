package adapters

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const operationsHelper = "/run/np1-control-native/operations-helper"

// Updated by the reproducible helper build, before packaging the Go binary.
const operationsHelperHash = "4fce73cf37e2e939ac722a06b952cfb1ff94fbd3dde0b999ec1f674f681130fe"

type NativeOperationOption struct {
	Value uint32 `json:"value"`
	Label string `json:"label"`
}
type NativeOperationItem struct {
	ID       string                  `json:"id"`
	Label    string                  `json:"label"`
	Field    string                  `json:"field"`
	Options  []NativeOperationOption `json:"options,omitempty"`
	Writable bool                    `json:"writable"`
}
type NativeOperationGroup struct {
	ID    string                `json:"id"`
	Label string                `json:"label"`
	Items []NativeOperationItem `json:"items"`
}

func operationOptions(labels ...string) []NativeOperationOption {
	out := []NativeOperationOption{}
	for i, label := range labels {
		out = append(out, NativeOperationOption{uint32(i + 1), label})
	}
	return out
}
func nativeOperationGroups() []NativeOperationGroup {
	quality := operationOptions("高画質 1080p", "長時間 720p")
	onOff := operationOptions("OFF", "ON")
	impact := operationOptions("1 · 低い", "2", "3 · 標準", "4", "5 · 高い")
	return []NativeOperationGroup{
		{"recorder", "ドライブレコーダー", []NativeOperationItem{{"recorder-front", "前方カメラの録画品質", "field1", quality, true}, {"recorder-cabin", "車内・後方カメラの録画品質", "field2", quality, true}, {"recorder-audio", "録画に車内の音声を含める", "field3", onOff, true}, {"recorder-impact", "イベント録画する衝撃の感度", "field4", impact, true}, {"recorder-overlay", "録画に日時・速度・位置情報を含める", "field5", onOff, true}}},
		{"parking", "駐車中の録画", []NativeOperationItem{{"parking-impact", "駐車中に録画する衝撃の感度", "field1", impact, true}, {"parking-security", "駐車監視", "field2", onOff, true}, {"parking-upload", "駐車監視の録画時間", "field3", operationOptions("30秒", "1分", "3分"), true}}},
		{"vehicle", "車両・マイク", []NativeOperationItem{{"vehicle-type", "車種", "field1", operationOptions("セダン", "コンパクトカー", "SUV", "ミニバン", "軽自動車", "2シーター"), true}, {"collection-area", "車内の広さ", "field2", operationOptions("狭い", "広い"), true}, {"collection-direction", "マイクの集音方向", "field3", operationOptions("左", "前方", "右"), true}}},
		{"installation", "取付情報", []NativeOperationItem{{"installation-direction", "取付方向", "field1", operationOptions("左側", "右側"), true}, {"installation-height", "取付高さ", "field2", nil, false}, {"installation-position", "取付位置", "field3", nil, false}}},
		{"volume", "本体の音量", []NativeOperationItem{{"master-volume", "音量", "field1", []NativeOperationOption{{0, "0"}, {1, "1"}, {2, "2"}, {3, "3"}, {4, "4"}, {5, "5"}, {6, "6"}, {7, "7"}, {8, "8"}, {9, "9"}, {10, "10"}}, true}}},
		{"account", "アカウントの状態", []NativeOperationItem{{"login-state", "ログイン", "field1", operationOptions("ログイン済み", "未ログイン", "起動時の認証更新中"), false}}},
		{"initial-setup", "初期設定の状態", []NativeOperationItem{{"initial-setup-state", "初期設定", "field1", operationOptions("完了", "通信設定以外は完了", "未完了"), false}}},
		{"alignment", "取付チェック", []NativeOperationItem{{"installation-check", "取付設定の完了確認", "active", []NativeOperationOption{{0, "未完了"}, {1, "完了"}}, false}, {"installation-angle", "取付角度の判定", "angleOK", []NativeOperationOption{{0, "調整が必要"}, {1, "適正"}}, false}}},
	}
}
func nativeOperationSpec(id string) (NativeOperationGroup, NativeOperationItem, bool) {
	for _, group := range nativeOperationGroups() {
		for _, item := range group.Items {
			if item.ID == id && item.Writable {
				return group, item, true
			}
		}
	}
	return NativeOperationGroup{}, NativeOperationItem{}, false
}
func nativeOperationGroupKnown(id string) bool {
	if id == "storage" {
		return true
	}
	for _, g := range nativeOperationGroups() {
		if g.ID == id {
			return true
		}
	}
	return false
}

func HandleNativeOperationsCatalog(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	recordingJSON(w, 200, map[string]any{"groups": nativeOperationGroups(), "actions": nativeActionCatalog(), "available": nativeFirmwareMatches(operationsHelper, operationsHelperHash)})
}

// Root broker only proxies fixed paths to the separate native worker.
func HandleNativeOperationsRemote(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/native-operations")
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", NativeControlSocket)
	}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 25 * time.Second}
	req, e := http.NewRequest(r.Method, "http://native/operations"+path, io.LimitReader(r.Body, 8193))
	if e != nil {
		nativeError(w, 400, "INVALID_OPERATION", "操作を確認してください")
		return
	}
	response, e := client.Do(req)
	if e != nil {
		nativeError(w, 503, "NATIVE_OPERATION_UNAVAILABLE", "本体操作のサービスに接続できません")
		return
	}
	defer response.Body.Close()
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(response.StatusCode)
	io.Copy(w, io.LimitReader(response.Body, 65536))
}

type operationsRunner func(group string, args ...string) (map[string]uint32, error)

func operationsOwner(group string) (string, nativeSettingIdentity, error) {
	owner := "Setting"
	if group == "storage" || group == "alignment" {
		owner = "DriveRec"
	}
	if action, ok := nativeActionSpec(group); ok {
		owner = action.Owner
	}
	text, e := command("systemctl", "show", "app-@"+owner+".service", "-p", "MainPID", "-p", "InvocationID", "-p", "ActiveState", "-p", "SubState", "-p", "Job")
	if e != nil {
		return owner, nativeSettingIdentity{}, e
	}
	identity, e := parseNativeSettingOwner(text)
	return owner, identity, e
}
func parseOperationsOutput(data []byte) (map[string]uint32, error) {
	if len(data) > 4096 {
		return nil, errors.New("oversized operation output")
	}
	out := map[string]uint32{}
	cleanup := false
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line == "cleanup=matched; own-receiver-removed" {
			if cleanup {
				return nil, errors.New("duplicate cleanup")
			}
			cleanup = true
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			return nil, errors.New("invalid output")
		}
		if _, exists := out[key]; exists {
			return nil, errors.New("duplicate value")
		}
		if key != "seen" && key != "inserted" && key != "formatStatus" && key != "optionalCable" && key != "accepted" && key != "active" && key != "direction" && key != "angleOK" && !(len(key) == 6 && strings.HasPrefix(key, "field") && key[5] >= '1' && key[5] <= '7') {
			return nil, errors.New("unexpected output key")
		}
		n, e := strconv.ParseUint(value, 10, 32)
		if e != nil || strconv.FormatUint(n, 10) != value {
			return nil, errors.New("invalid operation value")
		}
		out[key] = uint32(n)
	}
	if !cleanup || len(out) == 0 {
		return nil, errors.New("operation cleanup unconfirmed")
	}
	return out, nil
}
func runOperations(group string, args ...string) (map[string]uint32, error) {
	if !nativeFirmwareMatches(operationsHelper, operationsHelperHash) {
		return nil, errors.New("operation helper unavailable")
	}
	_, owner, e := operationsOwner(group)
	if e != nil {
		return nil, e
	}
	dir, e := os.MkdirTemp("/run/np1-control-native", "operations-")
	if e != nil {
		return nil, e
	}
	stderr, e := os.OpenFile(filepath.Join(dir, "error.log"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return nil, e
	}
	defer stderr.Close()
	stdout, e := os.OpenFile(filepath.Join(dir, "response.txt"), os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if e != nil {
		return nil, e
	}
	defer stdout.Close()
	// Fixed argv only. Never kill while the SDK owns a mutex; its own bounded
	// request deadline releases the receiver and acquired references on failure.
	argv := append([]string{args[0], owner.pid}, args[1:]...)
	cmd := exec.Command(operationsHelper, argv...)
	cmd.Env = []string{"PATH=/usr/bin:/bin", "LD_LIBRARY_PATH=/usr/local/lib", "NCC_NATIVE_OPERATIONS=1"}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if e = cmd.Run(); e != nil {
		return nil, e
	}
	_, after, e := operationsOwner(group)
	if e != nil || after != owner {
		return nil, errors.New("owner changed during operation")
	}
	stdout.Seek(0, 0)
	data, e := io.ReadAll(io.LimitReader(stdout, 4097))
	if e != nil {
		return nil, e
	}
	return parseOperationsOutput(data)
}

type formatCard struct {
	Fingerprint   string
	CapacityBytes uint64 `json:"capacityBytes"`
}

func sdFormatCardAt(root string) (formatCard, error) {
	names, e := filepath.Glob(filepath.Join(root, "mmcblk[0-9]"))
	if e != nil {
		return formatCard{}, e
	}
	found := []formatCard{}
	for _, name := range names {
		kind, e := os.ReadFile(filepath.Join(name, "device/type"))
		if e != nil || strings.TrimSpace(string(kind)) != "SD" {
			continue
		}
		cid, e := os.ReadFile(filepath.Join(name, "device/cid"))
		if e != nil {
			return formatCard{}, e
		}
		id := strings.TrimSpace(string(cid))
		if len(id) != 32 {
			return formatCard{}, errors.New("invalid SD identity")
		}
		if _, e = hex.DecodeString(id); e != nil {
			return formatCard{}, e
		}
		size, e := os.ReadFile(filepath.Join(name, "size"))
		if e != nil {
			return formatCard{}, e
		}
		sectors, e := strconv.ParseUint(strings.TrimSpace(string(size)), 10, 64)
		if e != nil || sectors == 0 || sectors > 1<<40 {
			return formatCard{}, errors.New("invalid SD capacity")
		}
		digest := sha256.Sum256([]byte(name + ":" + id + ":" + strings.TrimSpace(string(size))))
		found = append(found, formatCard{hex.EncodeToString(digest[:]), sectors * 512})
	}
	if len(found) != 1 {
		return formatCard{}, errors.New("unique SD card unavailable")
	}
	return found[0], nil
}

type formatTicket struct {
	card    formatCard
	expires time.Time
}
type nativeOperationsController struct {
	run     operationsRunner
	action  actionRunner
	card    func() (formatCard, error)
	now     func() time.Time
	mu      sync.Mutex
	tickets map[string]formatTicket
}

var nativeOperations = &nativeOperationsController{run: runOperations, card: func() (formatCard, error) { return sdFormatCardAt("/sys/class/block") }, now: time.Now, tickets: map[string]formatTicket{}}

func decodeOperation(w http.ResponseWriter, r *http.Request, out any) bool {
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192))
	d.DisallowUnknownFields()
	if d.Decode(out) != nil || d.Decode(&struct{}{}) != io.EOF {
		nativeError(w, 400, "INVALID_OPERATION", "操作内容を確認してください")
		return false
	}
	return true
}
func (c *nativeOperationsController) handle(w http.ResponseWriter, r *http.Request) {
	if !c.mu.TryLock() {
		nativeError(w, 409, "OPERATION_BUSY", "別の本体操作を確認中です")
		return
	}
	defer c.mu.Unlock()
	path := strings.TrimPrefix(r.URL.Path, "/operations/")
	w.Header().Set("Cache-Control", "no-store")
	if strings.HasPrefix(path, "actions/") {
		c.handleAction(w, r, strings.TrimPrefix(path, "actions/"))
		return
	}
	failure := func() {
		nativeError(w, 503, "OPERATION_UNCONFIRMED", "本体の応答を確認できません。状態を更新してください。操作を自動で再送しません。")
	}
	if r.Method == "GET" && strings.HasPrefix(path, "state/") {
		group := strings.TrimPrefix(path, "state/")
		if !nativeOperationGroupKnown(group) {
			nativeError(w, 404, "UNKNOWN_OPERATION", "対応していない操作です")
			return
		}
		value, e := c.run(group, "--get", group)
		if e != nil {
			failure()
			return
		}
		recordingJSON(w, 200, map[string]any{"values": value, "observedAt": c.now().UTC()})
		return
	}
	if r.Method == "PUT" && strings.HasPrefix(path, "settings/") {
		id := strings.TrimPrefix(path, "settings/")
		group, item, ok := nativeOperationSpec(id)
		if !ok {
			nativeError(w, 404, "UNKNOWN_OPERATION", "対応していない操作です")
			return
		}
		var body struct {
			ExpectedValue *uint32 `json:"expectedValue"`
			Value         *uint32 `json:"value"`
		}
		if !decodeOperation(w, r, &body) {
			return
		}
		if body.Value == nil || body.ExpectedValue == nil {
			nativeError(w, 400, "INVALID_OPERATION", "変更前後の値が必要です")
			return
		}
		valid := false
		for _, option := range item.Options {
			if option.Value == *body.Value {
				valid = true
			}
		}
		if !valid {
			nativeError(w, 400, "INVALID_OPERATION", "選択肢を確認してください")
			return
		}
		value, e := c.run(group.ID, "--set", id, strconv.FormatUint(uint64(*body.ExpectedValue), 10), strconv.FormatUint(uint64(*body.Value), 10))
		if e != nil || value[item.Field] != *body.Value {
			failure()
			return
		}
		recordingJSON(w, 200, map[string]any{"values": value, "observedAt": c.now().UTC(), "readback": true})
		return
	}
	if r.Method == "POST" && path == "sd-format/prepare" {
		var body struct{}
		if !decodeOperation(w, r, &body) {
			return
		}
		state, e := c.run("storage", "--get", "storage")
		if e != nil || state["inserted"] != 1 || state["formatStatus"] != 2 {
			nativeError(w, 409, "SD_NOT_READY", "SDカードが未挿入、処理中、または状態不明です")
			return
		}
		card, e := c.card()
		if e != nil {
			nativeError(w, 409, "SD_IDENTITY_UNAVAILABLE", "対象のSDカードを特定できません")
			return
		}
		for token, ticket := range c.tickets {
			if !ticket.expires.After(c.now()) {
				delete(c.tickets, token)
			}
		}
		if len(c.tickets) >= 32 {
			nativeError(w, 409, "FORMAT_CONFIRMATION_BUSY", "確認画面を閉じて少し待ってください")
			return
		}
		nonce := make([]byte, 32)
		if _, e = rand.Read(nonce); e != nil {
			failure()
			return
		}
		token := hex.EncodeToString(nonce)
		expires := c.now().Add(time.Minute)
		c.tickets[token] = formatTicket{card, expires}
		recordingJSON(w, 200, map[string]any{"token": token, "expiresAt": expires.UTC(), "capacityBytes": card.CapacityBytes})
		return
	}
	if r.Method == "POST" && path == "sd-format/execute" {
		var body struct {
			Token               string `json:"token"`
			Confirmation        string `json:"confirmation"`
			AcknowledgeDataLoss bool   `json:"acknowledgeDataLoss"`
		}
		if !decodeOperation(w, r, &body) {
			return
		}
		if body.Confirmation != "SDカードを消去" || !body.AcknowledgeDataLoss {
			nativeError(w, 400, "CONFIRMATION_REQUIRED", "SDカードの全データ消去の確認が必要です")
			return
		}
		ticket, ok := c.tickets[body.Token]
		delete(c.tickets, body.Token)
		card, e := c.card()
		if !ok || !ticket.expires.After(c.now()) || e != nil || card != ticket.card {
			nativeError(w, 409, "SD_CONFIRMATION_EXPIRED", "SDカードまたは確認期限が変わりました。対象を確認し直してください")
			return
		}
		value, e := c.run("storage", "--format", "ERASE_SD", ticket.card.Fingerprint)
		if e != nil || value["accepted"] != 1 {
			failure()
			return
		}
		// Acceptance is distinct from completion. Only DriveRec owns formatting.
		recordingJSON(w, 202, map[string]any{"accepted": true, "completionConfirmed": false, "values": value, "message": "本体がフォーマット要求を受け付けました。カードを抜かず、本体の案内と処理状態を確認してください。"})
		return
	}
	nativeError(w, 404, "UNKNOWN_OPERATION", "対応していない操作です")
}
func HandleNativeOperationsLocal(w http.ResponseWriter, r *http.Request) {
	if !nativeControlMu.TryLock() {
		nativeError(w, 409, "OPERATION_BUSY", "別の純正操作が進行中です")
		return
	}
	defer nativeControlMu.Unlock()
	release, e := lockNativeControl()
	if e != nil {
		nativeError(w, 409, "OPERATION_BUSY", "純正設定のサービスを更新または操作中です")
		return
	}
	defer release()
	nativeOperations.handle(w, r)
}
