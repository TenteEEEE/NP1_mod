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
	stockBLEEventPath      = dbus.ObjectPath("/SPF/EventBase/Evt048")
	stockBLEInterface      = "SPF.EventBase.v1_0"
	stockBLEEvent          = stockBLEInterface + ".SPFEvent"
	stockDiscoveryFunction = 15
	// The controller filters repeated reports from the same address within a scan.
	stockDiscoverySeconds = 1
	// Longer windows cover more of the radio schedule when no reports come back.
	stockQuietSeconds = 5
	stockScanSpacing  = 40 * time.Millisecond
)

var errStockDiscoveryRejected = errors.New("純正BluetoothがBLE探索を受け付けません（純正側のBLE準備または探索状態を確認してください）")

// RegisterStockReceiver is only mounted on the local broker's UID-checked
// Unix socket. The endpoint takes no Bluetooth commands or parameters.
func RegisterStockReceiver(mux *http.ServeMux) {
	var session sync.Mutex
	var stockLease time.Time
	seed := maphash.MakeSeed()
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
		if delay := time.Until(stockLease); delay > 0 && !waitStockScan(ctx, delay) {
			return
		}
		err := scanStockBLEWith(ctx, &stockLease, stockAdvertisement, func() { write(receiverMessage{Ready: true}) }, func(source string, payload []byte) {
			write(receiverMessage{Source: strconv.FormatUint(maphash.String(seed, source), 16), Payload: payload, AtMS: time.Now().UnixMilli()})
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

type advertisementDecoder func([]byte) (source string, payload []byte, done bool)

type stockBLESession struct {
	conn   *dbus.Conn
	owner  string
	pid    uint32
	sender string
}

func scanStockBLEWith(ctx context.Context, lease *time.Time, decode advertisementDecoder, ready func(), receive func(string, []byte)) error {
	session, err := connectStockBLE(ctx)
	if err != nil {
		return err
	}
	defer session.conn.Close()
	signals := make(chan *dbus.Signal, 128)
	session.conn.Signal(signals)
	defer session.conn.RemoveSignal(signals)
	if err := session.watchDiscovery(ctx); err != nil {
		return err
	}
	caller := uint32(os.Getpid())
	object := session.conn.Object(session.owner, stockBLEPath)
	return runStockDiscovery(ctx, lease, func(ctx context.Context, seconds uint32) error {
		return startStockDiscovery(ctx, object, caller, seconds)
	}, signals,
		func(signal *dbus.Signal) ([]byte, bool) { return session.discoveryData(ctx, signal, caller) }, decode, ready, receive)
}

func connectStockBLE(ctx context.Context) (*stockBLESession, error) {
	info, err := os.Lstat(stockBusFile)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o022 != 0 {
		return nil, errors.New("純正Bluetoothのセッションバスが利用できません")
	}
	file, err := os.Open(stockBusFile)
	if err != nil {
		return nil, errors.New("純正Bluetoothのセッションバスが利用できません")
	}
	data, err := io.ReadAll(io.LimitReader(file, 513))
	_ = file.Close()
	if err != nil || len(data) > 512 {
		return nil, errors.New("純正Bluetoothのセッションバスを識別できません")
	}
	address := stockBusAddress(string(data))
	if address == "" {
		return nil, errors.New("純正Bluetoothのセッションバスを識別できません")
	}
	conn, err := dbus.Connect(address, dbus.WithContext(ctx), dbus.WithSignalHandler(dbus.NewSequentialSignalHandler()))
	if err != nil {
		return nil, errors.New("純正Bluetoothのセッションバスに接続できません")
	}
	keep := false
	defer func() {
		if !keep {
			_ = conn.Close()
		}
	}()
	setup, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	var owner string
	if conn.BusObject().CallWithContext(setup, "org.freedesktop.DBus.GetNameOwner", 0, stockBLEName).Store(&owner) != nil {
		return nil, errors.New("純正BluetoothのBLEサービスが見つかりません")
	}
	var pid uint32
	if conn.BusObject().CallWithContext(setup, "org.freedesktop.DBus.GetConnectionUnixProcessID", 0, owner).Store(&pid) != nil {
		return nil, errors.New("純正Bluetoothのサービス所有者を確認できません")
	}
	executable, err := os.Readlink("/proc/" + strconv.FormatUint(uint64(pid), 10) + "/exe")
	if err != nil || executable != stockOwnerExecutable {
		return nil, errors.New("Bluetoothの所有者が純正サービスと一致しません")
	}
	keep = true
	return &stockBLESession{conn: conn, owner: owner, pid: pid}, nil
}

func (session *stockBLESession) watchDiscovery(ctx context.Context) error {
	subscribe, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	if session.conn.AddMatchSignalContext(subscribe, dbus.WithMatchObjectPath(stockBLEEventPath), dbus.WithMatchInterface(stockBLEInterface), dbus.WithMatchMember("SPFEvent")) != nil {
		return errors.New("純正Bluetoothの広告通知を購読できません")
	}
	return nil
}

func (session *stockBLESession) discoveryData(ctx context.Context, signal *dbus.Signal, caller uint32) ([]byte, bool) {
	data, ok := stockDiscoveryEvent(signal, caller)
	if !ok {
		return nil, false
	}
	// The stock process can publish events on a separate D-Bus connection.
	// Match its authenticated process ID before consuming any reports.
	if signal.Sender != session.owner && (session.sender == "" || signal.Sender != session.sender) {
		if !strings.HasPrefix(signal.Sender, ":") {
			return nil, false
		}
		check, cancel := context.WithTimeout(ctx, 250*time.Millisecond)
		defer cancel()
		var pid uint32
		if session.conn.BusObject().CallWithContext(check, "org.freedesktop.DBus.GetConnectionUnixProcessID", 0, signal.Sender).Store(&pid) != nil || pid != session.pid {
			return nil, false
		}
		session.sender = signal.Sender
	}
	return data, true
}

func startStockDiscovery(ctx context.Context, object dbus.BusObject, caller, seconds uint32) error {
	request, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	// The vendor API's passive discovery request expires on its own timer.
	// There is no per-client StopDiscovery, so never issue the global stop,
	// initialize GATT or retry before the completion callback from this request.
	var payload [24]byte
	binary.LittleEndian.PutUint32(payload[4:8], stockDiscoveryFunction)
	binary.LittleEndian.PutUint32(payload[8:12], stockDiscoveryFunction)
	binary.LittleEndian.PutUint32(payload[16:20], seconds)
	var result []byte
	var returned []uint32
	if object.CallWithContext(request, stockBLEInterface+".CallFunction", 0, uint32(1), caller, uint32(stockDiscoveryFunction), []uint32{}, payload[:], uint32(1)).Store(&result, &returned) != nil || len(result) != 1 {
		return errors.New("純正BluetoothのBLE探索要求に応答がありません")
	}
	if result[0] != 1 {
		return errStockDiscoveryRejected
	}
	return nil
}

func runStockDiscovery(ctx context.Context, lease *time.Time, start func(context.Context, uint32) error, signals <-chan *dbus.Signal, read func(*dbus.Signal) ([]byte, bool), decode advertisementDecoder, ready func(), receive func(string, []byte)) error {
	confirmed := false
	seconds := uint32(stockDiscoverySeconds)
	for ctx.Err() == nil {
		window := time.Duration(seconds) * time.Second
		// An in-flight request may have started even if its acknowledgement is lost.
		// An unanswered request can still start late. Reserve time for both the
		// acknowledgement and the controller's delayed completion.
		*lease = time.Now().Add(window + 3*time.Second)
		if err := start(ctx, seconds); err != nil {
			if errors.Is(err, errStockDiscoveryRejected) {
				*lease = time.Time{}
			}
			return err
		}
		timeout, cancel := context.WithTimeout(ctx, window+2*time.Second)
		complete := false
		reported := false
		for !complete && timeout.Err() == nil {
			select {
			case <-timeout.Done():
			case signal, ok := <-signals:
				if !ok {
					cancel()
					return errors.New("純正Bluetoothの通知が切断されました")
				}
				data, ok := read(signal)
				if !ok {
					continue
				}
				source, payload, done := decode(data)
				complete = done
				if source != "" {
					reported = true
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
		if complete {
			// Only a clean callback allows the next bounded scan to start quickly.
			*lease = time.Now().Add(stockScanSpacing)
			seconds = stockQuietSeconds
			if reported {
				seconds = stockDiscoverySeconds
			}
			ready()
			confirmed = true
			waitStockScan(ctx, time.Until(*lease))
		}
	}
	return ctx.Err()
}

func waitStockScan(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func stockDiscoveryEvent(signal *dbus.Signal, caller uint32) ([]byte, bool) {
	if signal == nil || signal.Name != stockBLEEvent || signal.Path != stockBLEEventPath || len(signal.Body) != 3 || signal.Body[0] != caller || signal.Body[1] != uint32(0) {
		return nil, false
	}
	data, ok := signal.Body[2].([]byte)
	if !ok || len(data) < 12 || binary.LittleEndian.Uint64(data[:8]) != 0x2c8 || binary.LittleEndian.Uint32(data[8:12]) != 0x30 {
		return nil, false
	}
	return data[12:], true
}

func stockAdvertisement(data []byte) (source string, payload []byte, done bool) {
	return stockAdvertisementFor(data, Manufacturer)
}

func stockAdvertisementFor(data []byte, manufacturer uint16) (source string, payload []byte, done bool) {
	// NPBTLEDiscoveryResponseEvent::Pack emits a 0x298-byte fixed body,
	// followed by the dedicated manufacturer value and additional AD values.
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
	return source, stockManufacturerData(data[:fixedSize], data[fixedSize:], manufacturer), false
}

func stockManufacturerData(header, values []byte, manufacturer uint16) []byte {
	match := func(value []byte) bool {
		return len(value) >= 12 && len(value) <= 29 && binary.LittleEndian.Uint16(value) == manufacturer
	}
	length := int(header[0x1ec])
	if length > len(values) || length > 31 {
		return nil
	}
	if match(values[:length]) {
		return values[2:length]
	}
	values = values[length:]
	count := int(header[0x1fc])
	if count > 8 {
		return nil
	}
	// The stock extended scanner can put the entire manufacturer AD value in
	// its variable AD list (type 0xff), with no dedicated manufacturer value.
	for i := 0; i < count; i++ {
		entry := 0x200 + 0x10*i
		length := int(header[entry+1])
		if length > len(values) || length > 31 {
			return nil
		}
		if header[entry] == 0xff && match(values[:length]) {
			return values[2:length]
		}
		values = values[length:]
	}
	return nil
}
