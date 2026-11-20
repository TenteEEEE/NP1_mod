package pics

import (
	"bytes"
	"context"
	"encoding/binary"
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
	return &dbus.Signal{Name: stockBLEEvent, Path: stockBLEPath, Body: []any{caller, uint32(0), append(frame, data...)}}
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
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	signals := make(chan *dbus.Signal, 3)
	signals <- vendorEvent(caller+1, completed)
	signals <- vendorEvent(caller, data)
	signals <- vendorEvent(caller, completed)
	stop := errors.New("done")
	started, ready, received := 0, 0, 0
	err := runStockDiscovery(ctx, func(context.Context) error {
		started++
		if started == 2 {
			return stop
		}
		return nil
	}, signals, caller, func() { ready++ }, func(_ string, data []byte) {
		received++
		if !bytes.Equal(data, want) {
			t.Errorf("payload = %x", data)
		}
	})
	if !errors.Is(err, stop) || started != 2 || ready != 1 || received != 1 {
		t.Fatalf("start=%d ready=%d received=%d err=%v", started, ready, received, err)
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
		w.Write([]byte("{\"ready\":true}\n{\"source\":\"test\",\"payload\":\"AAACAAAAAAAAAYM=\"}\n{\"error\":\"scanner unavailable\"}\n"))
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
	err := Scan(client)(context.Background(), func() { ready = true }, func(source string, data []byte) { monitor.Receive(source, data, time.Now()) })
	if err == nil || err.Error() != "scanner unavailable" || !ready {
		t.Fatalf("ready=%v err=%v", ready, err)
	}
	got := monitor.Signals(time.Now())
	if len(got) != 1 || got[0].Color != Green {
		t.Fatalf("signal = %#v", got)
	}
}

type stockTestTransport func(*http.Request) (*http.Response, error)

func (f stockTestTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
