package pics

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"hash/maphash"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/godbus/dbus/v5"
)

const (
	stockBusFile           = "/tmp/dbus_session_bus_address"
	stockOwnerExecutable   = "/usr/local/bin/NEventWatcherCom"
	stockBLEName           = "SPF.EventBase.NPBTLEHandler.Stub"
	stockBLEPath           = dbus.ObjectPath("/SPF/EventBase/NPBTLEHandler")
	stockBLEInterface      = "SPF.EventBase.v1_0"
	stockBLEEvent          = stockBLEInterface + ".SPFEvent"
	stockDiscoveryFunction = 15
	stockDiscoverySeconds  = 2
)

// RegisterStockReceiver is only mounted on the local broker's UID-checked
// Unix socket. The endpoint takes no Bluetooth commands or parameters.
func RegisterStockReceiver(mux *http.ServeMux) {
	var session sync.Mutex
	mux.HandleFunc("GET "+BrokerPath, func(w http.ResponseWriter, r *http.Request) {
		if !session.TryLock() {
			http.Error(w, "stock receiver already in use", http.StatusConflict)
			return
		}
		defer session.Unlock()
		response := http.NewResponseController(w)
		w.Header().Set("Content-Type", "application/x-ndjson")
		w.Header().Set("Cache-Control", "no-store")
		if response.Flush() != nil {
			return
		}
		encoder := json.NewEncoder(w)
		ctx, cancel := context.WithCancel(r.Context())
		defer cancel()
		write := func(event receiverMessage) {
			_ = response.SetWriteDeadline(time.Now().Add(time.Second))
			if encoder.Encode(event) != nil || response.Flush() != nil {
				cancel()
			}
			_ = response.SetWriteDeadline(time.Time{})
		}
		seed := maphash.MakeSeed()
		err := scanStockBLE(ctx, func() { write(receiverMessage{Ready: true}) }, func(source string, payload []byte) {
			write(receiverMessage{Source: strconv.FormatUint(maphash.String(seed, source), 16), Payload: payload})
		})
		if err != nil && ctx.Err() == nil {
			write(receiverMessage{Error: err.Error()})
		}
	})
}

func stockBusAddress(contents string) string {
	for _, line := range strings.Split(contents, "\n") {
		value, ok := strings.CutPrefix(line, "DBUS_SESSION_BUS_ADDRESS='")
		if !ok {
			continue
		}
		addr, suffix, ok := strings.Cut(value, "'")
		if !ok || (strings.TrimSpace(suffix) != "" && strings.TrimSpace(suffix) != ";" && strings.TrimSpace(suffix) != "; export DBUS_SESSION_BUS_ADDRESS;") || strings.ContainsAny(addr, ";\r\t ") {
			return ""
		}
		if strings.HasPrefix(addr, "unix:abstract=") || strings.HasPrefix(addr, "unix:path=/tmp/") {
			return addr
		}
	}
	return ""
}

func scanStockBLE(ctx context.Context, ready func(), receive func(string, []byte)) error {
	info, err := os.Lstat(stockBusFile)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o022 != 0 {
		return errors.New("純正Bluetoothのセッションバスが利用できません")
	}
	file, err := os.Open(stockBusFile)
	if err != nil {
		return errors.New("純正Bluetoothのセッションバスが利用できません")
	}
	data, err := io.ReadAll(io.LimitReader(file, 513))
	_ = file.Close()
	if err != nil || len(data) > 512 {
		return errors.New("純正Bluetoothのセッションバスを識別できません")
	}
	address := stockBusAddress(string(data))
	if address == "" {
		return errors.New("純正Bluetoothのセッションバスを識別できません")
	}
	conn, err := dbus.Connect(address, dbus.WithContext(ctx))
	if err != nil {
		return errors.New("純正Bluetoothのセッションバスに接続できません")
	}
	defer conn.Close()
	setup, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	var owner string
	if conn.BusObject().CallWithContext(setup, "org.freedesktop.DBus.GetNameOwner", 0, stockBLEName).Store(&owner) != nil {
		return errors.New("純正BluetoothのBLEサービスが見つかりません")
	}
	var pid uint32
	if conn.BusObject().CallWithContext(setup, "org.freedesktop.DBus.GetConnectionUnixProcessID", 0, owner).Store(&pid) != nil {
		return errors.New("純正Bluetoothのサービス所有者を確認できません")
	}
	executable, err := os.Readlink("/proc/" + strconv.FormatUint(uint64(pid), 10) + "/exe")
	if err != nil || executable != stockOwnerExecutable {
		return errors.New("Bluetoothの所有者が純正サービスと一致しません")
	}
	signals := make(chan *dbus.Signal, 32)
	conn.Signal(signals)
	defer conn.RemoveSignal(signals)
	if conn.AddMatchSignalContext(setup, dbus.WithMatchSender(owner), dbus.WithMatchObjectPath(stockBLEPath), dbus.WithMatchInterface(stockBLEInterface), dbus.WithMatchMember("SPFEvent")) != nil {
		return errors.New("純正Bluetoothの広告通知を購読できません")
	}
	caller := uint32(os.Getpid())
	object := conn.Object(owner, stockBLEPath)
	return runStockDiscovery(ctx, func(ctx context.Context) error { return startStockDiscovery(ctx, object, caller) }, signals, caller, ready, receive)
}

func startStockDiscovery(ctx context.Context, object dbus.BusObject, caller uint32) error {
	request, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	// The vendor API's passive discovery request expires in two seconds.
	// There is no per-client StopDiscovery, so never issue the global stop,
	// initialize GATT or retry before the completion callback from this request.
	var payload [24]byte
	binary.LittleEndian.PutUint32(payload[4:8], stockDiscoveryFunction)
	binary.LittleEndian.PutUint32(payload[8:12], stockDiscoveryFunction)
	binary.LittleEndian.PutUint32(payload[16:20], stockDiscoverySeconds)
	var result []byte
	var returned []uint32
	if object.CallWithContext(request, stockBLEInterface+".CallFunction", 0, uint32(1), caller, uint32(stockDiscoveryFunction), []uint32{}, payload[:], uint32(1)).Store(&result, &returned) != nil || len(result) != 1 {
		return errors.New("純正BluetoothのBLE探索要求に応答がありません")
	}
	if result[0] != 1 {
		return errors.New("純正BluetoothがBLE探索を受け付けません（純正側のBLE準備または探索状態を確認してください）")
	}
	return nil
}

func runStockDiscovery(ctx context.Context, start func(context.Context) error, signals <-chan *dbus.Signal, caller uint32, ready func(), receive func(string, []byte)) error {
	confirmed := false
	for ctx.Err() == nil {
		if err := start(ctx); err != nil {
			return err
		}
		timeout, cancel := context.WithTimeout(ctx, 4*time.Second)
		complete := false
		for !complete && timeout.Err() == nil {
			select {
			case <-timeout.Done():
			case signal, ok := <-signals:
				if !ok {
					cancel()
					return errors.New("純正Bluetoothの通知が切断されました")
				}
				data, ok := stockDiscoveryEvent(signal, caller)
				if !ok {
					continue
				}
				source, payload, done := stockAdvertisement(data)
				complete = done
				if source != "" {
					if !confirmed {
						ready()
						confirmed = true
					}
					if payload != nil {
						receive(source, payload)
					}
				}
			}
		}
		cancel()
		if !complete && ctx.Err() == nil {
			return errors.New("純正BluetoothからBLE探索の終了通知がありません")
		}
		// Leave room for the stock caller between our self-expiring scans.
		timer := time.NewTimer(200 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
		case <-timer.C:
		}
	}
	return ctx.Err()
}

func stockDiscoveryEvent(signal *dbus.Signal, caller uint32) ([]byte, bool) {
	if signal == nil || signal.Name != stockBLEEvent || signal.Path != stockBLEPath || len(signal.Body) != 3 || signal.Body[0] != caller || signal.Body[1] != uint32(0) {
		return nil, false
	}
	data, ok := signal.Body[2].([]byte)
	if !ok || len(data) < 12 || binary.LittleEndian.Uint64(data[:8]) != 0x2c8 || binary.LittleEndian.Uint32(data[8:12]) != 0x30 {
		return nil, false
	}
	return data[12:], true
}

func stockAdvertisement(data []byte) (source string, payload []byte, done bool) {
	// NPBTLEDiscoveryResponseEvent::Pack emits a 0x298-byte fixed body,
	// followed by the manufacturer AD value and any service-data values.
	const fixedSize = 0x298
	if len(data) < fixedSize {
		return "", nil, false
	}
	if marker := data[0x291]; marker != 0 {
		return "", nil, marker == 1 || marker == 2
	}
	address := [6]byte(data[0x20:0x26])
	if address == [6]byte{} {
		return "", nil, false
	}
	source = string(address[:])
	length := int(data[0x1ec])
	if length >= 12 && length <= 29 && fixedSize+length <= len(data) && binary.LittleEndian.Uint16(data[fixedSize:]) == Manufacturer {
		payload = data[fixedSize+2 : fixedSize+length]
	}
	return source, payload, false
}
