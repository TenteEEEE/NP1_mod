package pics

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
)

func vendorEvent(caller uint32, data []byte) *dbus.Signal {
	frame := make([]byte, 12, 12+len(data))
	binary.LittleEndian.PutUint64(frame, 0x2c8)
	binary.LittleEndian.PutUint32(frame[8:], 0x30)
	return &dbus.Signal{Name: stockBLEEvent, Path: stockBLEEventPath, Body: []any{caller, uint32(0), append(frame, data...)}}
}

func TestStockDiscoveryOnlyDeliversManufacturerDataFromOwnRequest(t *testing.T) {
	const caller = uint32(100)
	want := frame(1, 0x83)
	data := make([]byte, 0x298)
	copy(data[0x20:], []byte{1, 2, 3, 4, 5, 6})
	data[0x1ec] = byte(len(want) + 2)
	data = append(append(data, 0xce, 0x01), want...)
	if _, ok := stockDiscoveryEvent(vendorEvent(caller+1, data), caller); ok {
		t.Fatal("event for another caller")
	}
	requestPath := vendorEvent(caller, data)
	requestPath.Path = stockBLEPath
	if _, ok := stockDiscoveryEvent(requestPath, caller); ok {
		t.Fatal("accepted a notification on the request path")
	}
	if _, payload, _ := stockAdvertisement(data[:len(data)-1]); payload != nil {
		t.Fatal("truncated payload decoded")
	}
	data[0x298] = 0
	if _, payload, _ := stockAdvertisement(data); payload != nil {
		t.Fatal("foreign manufacturer decoded")
	}
	data[0x298] = 0xce
	completed := make([]byte, 0x298)
	completed[0x291] = 1
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	signals := make(chan *dbus.Signal, 3)
	signals <- vendorEvent(caller+1, completed)
	signals <- vendorEvent(caller, data)
	signals <- vendorEvent(caller, completed)
	stop := errors.New("done")
	started, ready, received := 0, 0, 0
	err := runStockDiscovery(ctx, new(time.Time), func(context.Context, uint32) error {
		started++
		if started == 2 {
			return stop
		}
		return nil
	}, signals, func(signal *dbus.Signal) ([]byte, bool) { return stockDiscoveryEvent(signal, caller) }, stockAdvertisement, func() { ready++ }, func(_ string, data []byte) {
		received++
		if !bytes.Equal(data, want) {
			t.Errorf("payload = %x", data)
		}
	})
	if !errors.Is(err, stop) || started != 2 || ready != 2 || received != 1 {
		t.Fatalf("start=%d ready=%d received=%d err=%v", started, ready, received, err)
	}
}

func TestStockAdvertisementFindsManufacturerInAdditionalADValues(t *testing.T) {
	want := frame(1, 0x83)
	data := make([]byte, 0x298)
	copy(data[0x20:], []byte{1, 2, 3, 4, 5, 6})
	data[0x1fc] = 2
	data[0x200], data[0x201] = 0x16, 3
	data[0x210], data[0x211] = 0xff, byte(len(want)+2)
	data = append(data, 0xce, 0x01, 0x00, 0xce, 0x01)
	data = append(data, want...)
	source, payload, done := stockAdvertisement(data)
	if source == "" || done || !bytes.Equal(payload, want) {
		t.Fatalf("advertisement incorrectly decoded: source present=%v, payload=%x, done=%v", source != "", payload, done)
	}
	if _, payload, _ := stockAdvertisement(data[:len(data)-1]); payload != nil {
		t.Fatal("accepted an incomplete manufacturer field")
	}
	data[0x210] = 0x16
	if _, payload, _ := stockAdvertisement(data); payload != nil {
		t.Fatal("accepted a different AD type with the PICS identifier")
	}
}

func TestStockDiscoveryCompletesWithNoNearbyAdvertisements(t *testing.T) {
	const caller = uint32(100)
	completed := make([]byte, 0x298)
	completed[0x291] = 1
	signals := make(chan *dbus.Signal, 1)
	signals <- vendorEvent(caller, completed)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ready := 0
	err := runStockDiscovery(ctx, new(time.Time), func(context.Context, uint32) error { return nil }, signals, func(signal *dbus.Signal) ([]byte, bool) { return stockDiscoveryEvent(signal, caller) }, stockAdvertisement, func() {
		ready++
		cancel()
	}, func(string, []byte) { t.Error("unexpected advertisement") })
	if ready != 1 || !errors.Is(err, context.Canceled) {
		t.Fatalf("empty scan did not confirm reception readiness: ready=%d, err=%v", ready, err)
	}
}

func TestStockDiscoveryLengthensAnEmptyWindowAndResumesShortScans(t *testing.T) {
	const caller = uint32(100)
	completed := make([]byte, 0x298)
	completed[0x291] = 1
	otherDevice := make([]byte, 0x298)
	otherDevice[0x20] = 1
	signals := make(chan *dbus.Signal, 2)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	want := [...]uint32{stockDiscoverySeconds, stockQuietSeconds, stockDiscoverySeconds}
	attempt := 0
	err := runStockDiscovery(ctx, new(time.Time), func(_ context.Context, seconds uint32) error {
		if attempt >= len(want) || seconds != want[attempt] {
			t.Fatalf("unexpected scan %d with %d seconds", attempt, seconds)
		}
		attempt++
		if attempt == 2 {
			signals <- vendorEvent(caller, otherDevice)
		}
		if attempt == len(want) {
			return context.Canceled
		}
		signals <- vendorEvent(caller, completed)
		return nil
	}, signals, func(signal *dbus.Signal) ([]byte, bool) { return stockDiscoveryEvent(signal, caller) }, stockAdvertisement,
		func() {}, func(string, []byte) { t.Error("non-PICS advertisement was passed through") })
	if err != context.Canceled || attempt != len(want) {
		t.Fatalf("attempts=%d: %v", attempt, err)
	}
}

func TestStockDiscoveryClearsLeaseOnlyForDefinitiveRejection(t *testing.T) {
	for _, err := range []error{errStockDiscoveryRejected, context.DeadlineExceeded} {
		var lease time.Time
		result := runStockDiscovery(context.Background(), &lease, func(context.Context, uint32) error { return err }, nil, nil, nil, nil, nil)
		if result != err || lease.IsZero() != (err == errStockDiscoveryRejected) {
			t.Fatalf("lease=%v error=%v", lease, result)
		}
	}
}

func TestStockBusAddressRejectsNonLocalTransport(t *testing.T) {
	for _, file := range []string{
		"DBUS_SESSION_BUS_ADDRESS='tcp:host=localhost,port=9000'\n",
		"DBUS_SESSION_BUS_ADDRESS='unix:abstract=local;tcp:host=localhost'\n",
		"DBUS_SESSION_BUS_ADDRESS='unix:path=/tmp/socket' && other\n",
	} {
		if stockBusAddress(file) != "" {
			t.Fatalf("accepted %q", file)
		}
	}
	if want := "unix:abstract=/tmp/test,guid=1234"; stockBusAddress("DBUS_SESSION_BUS_ADDRESS='"+want+"';\nDBUS_SESSION_BUS_PID=9;\n") != want {
		t.Fatal("stock address not decoded")
	}
}

func TestStockReceiverStreamFeedsTheMonitorAndPropagatesFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != BrokerPath {
			t.Errorf("stock path = %s", r.URL.Path)
		}
		send := json.NewEncoder(w)
		now := time.Now()
		for _, event := range []receiverMessage{
			{Ready: true},
			{Source: "old", Payload: frame(2, 0x81), AtMS: now.Add(-3 * time.Second).UnixMilli()},
			{Source: "future", Payload: frame(3, 0x81), AtMS: now.Add(3 * time.Second).UnixMilli()},
			{Source: "test", Payload: frame(1, 0x83), AtMS: now.Add(-200 * time.Millisecond).UnixMilli()},
			{Error: "scanner unavailable"},
		} {
			if err := send.Encode(event); err != nil {
				t.Error(err)
			}
		}
	}))
	defer server.Close()
	transport := server.Client().Transport
	client := &http.Client{Transport: stockTestTransport(func(r *http.Request) (*http.Response, error) {
		req := r.Clone(r.Context())
		req.URL.Host = server.Listener.Addr().String()
		if transport == nil {
			transport = http.DefaultTransport
		}
		return transport.RoundTrip(req)
	})}
	var monitor Monitor
	ready := false
	err := Scan(client)(context.Background(), func() { ready = true }, func(source string, data []byte, received time.Time) { monitor.Receive(source, data, received) })
	if err == nil || err.Error() != "scanner unavailable" || !ready {
		t.Fatalf("ready=%v err=%v", ready, err)
	}
	got := monitor.Signals(time.Now())
	if len(got) != 1 || got[0].Color != Green || got[0].FreshnessMS < 200 {
		t.Fatalf("signal = %#v", got)
	}
}

func TestStockReceiverRetriesWhenAnOpenBrokerStreamStopsReporting(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		flusher := w.(http.Flusher)
		_, _ = w.Write([]byte("{\"ready\":true}\n"))
		flusher.Flush()
		<-r.Context().Done()
	}))
	defer server.Close()
	client := &http.Client{Transport: stockTestTransport(func(r *http.Request) (*http.Response, error) {
		req := r.Clone(r.Context())
		req.URL.Host = server.Listener.Addr().String()
		return http.DefaultTransport.RoundTrip(req)
	})}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	confirmed := false
	err := scanReceiver(client, 120*time.Millisecond)(ctx, func() { confirmed = true }, func(string, []byte, time.Time) {})
	if !confirmed || !errors.Is(err, errStockStreamSilent) {
		t.Fatalf("open stream stalled without a retryable failure: ready=%v, err=%v", confirmed, err)
	}
}

type stockTestTransport func(*http.Request) (*http.Response, error)

func (f stockTestTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
