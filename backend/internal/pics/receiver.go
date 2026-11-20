package pics

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/godbus/dbus/v5"
)

const (
	bluez               = "org.bluez"
	adapterInterface    = bluez + ".Adapter1"
	deviceInterface     = bluez + ".Device1"
	objectsInterface    = "org.freedesktop.DBus.ObjectManager"
	propertiesInterface = "org.freedesktop.DBus.Properties"
)

// Scan opens a shared LE discovery session with BlueZ using the server's
// unprivileged account. It owns no pairing, controller or GATT connection.
func Scan(ctx context.Context, ready func(), receive func(string, []byte)) error {
	conn, err := dbus.ConnectSystemBus()
	if err != nil {
		return errors.New("本体のBluetooth受信インターフェースに接続できません")
	}
	defer conn.Close()
	setup, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	var owner string
	if conn.BusObject().CallWithContext(setup, "org.freedesktop.DBus.GetNameOwner", 0, bluez).Store(&owner) != nil {
		return errors.New("NP1内蔵Bluetooth用のBlueZ受信経路がありません")
	}
	signals := make(chan *dbus.Signal, 64)
	conn.Signal(signals)
	defer conn.RemoveSignal(signals)
	for _, pair := range [][2]string{{propertiesInterface, "PropertiesChanged"}, {objectsInterface, "InterfacesAdded"}, {objectsInterface, "InterfacesRemoved"}} {
		if err := conn.AddMatchSignalContext(setup, dbus.WithMatchSender(owner), dbus.WithMatchInterface(pair[0]), dbus.WithMatchMember(pair[1])); err != nil {
			return errors.New("Bluetoothの広告通知を購読できません")
		}
	}
	if err := conn.AddMatchSignalContext(setup, dbus.WithMatchSender("org.freedesktop.DBus"), dbus.WithMatchInterface("org.freedesktop.DBus"), dbus.WithMatchMember("NameOwnerChanged"), dbus.WithMatchArg(0, bluez)); err != nil {
		return errors.New("Bluetoothの稼働状態を監視できません")
	}
	var objects map[dbus.ObjectPath]map[string]map[string]dbus.Variant
	if conn.Object(owner, "/").CallWithContext(setup, objectsInterface+".GetManagedObjects", 0).Store(&objects) != nil {
		return errors.New("Bluetoothアダプターを取得できません")
	}
	// Only use the single controller that can be identified without changing
	// radio state or choosing an unrelated attached adapter.
	var path dbus.ObjectPath
	for candidate, interfaces := range objects {
		if _, ok := interfaces[adapterInterface]; !ok {
			continue
		}
		if path != "" {
			return errors.New("内蔵Bluetoothアダプターを特定できません")
		}
		path = candidate
	}
	if path == "" {
		return errors.New("本体にBluetoothアダプターが見つかりません")
	}
	adapter := conn.Object(owner, path)
	filter := map[string]dbus.Variant{"Transport": dbus.MakeVariant("le"), "DuplicateData": dbus.MakeVariant(true)}
	if adapter.CallWithContext(setup, adapterInterface+".SetDiscoveryFilter", 0, filter).Err != nil ||
		adapter.CallWithContext(setup, adapterInterface+".StartDiscovery", 0).Err != nil {
		return errors.New("内蔵BluetoothでBLEスキャンを開始できません（電源または権限を確認してください）")
	}
	defer func() {
		stop, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		adapter.CallWithContext(stop, adapterInterface+".StopDiscovery", 0)
	}()
	ready()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-conn.Context().Done():
			return errors.New("Bluetoothとの接続が切断されました")
		case event, ok := <-signals:
			if !ok || event.Name == "org.freedesktop.DBus.NameOwnerChanged" {
				return errors.New("Bluetoothの受信サービスが停止しました")
			}
			if event.Path == path && event.Name == propertiesInterface+".PropertiesChanged" && len(event.Body) >= 2 {
				if changed, ok := event.Body[1].(map[string]dbus.Variant); ok && changed["Powered"].Value() == false {
					return errors.New("Bluetoothの電源がオフです")
				}
			}
			if event.Name == objectsInterface+".InterfacesRemoved" && len(event.Body) >= 1 && event.Body[0] == path {
				return errors.New("Bluetoothアダプターが利用できなくなりました")
			}
			if source, payload := advertisement(event, path); payload != nil {
				receive(source, payload)
			}
		}
	}
}

func advertisement(event *dbus.Signal, adapter dbus.ObjectPath) (string, []byte) {
	if len(event.Body) < 2 {
		return "", nil
	}
	path := event.Path
	var data map[string]dbus.Variant
	switch event.Name {
	case propertiesInterface + ".PropertiesChanged":
		if event.Body[0] == deviceInterface {
			data, _ = event.Body[1].(map[string]dbus.Variant)
		}
	case objectsInterface + ".InterfacesAdded":
		path, _ = event.Body[0].(dbus.ObjectPath)
		if interfaces, ok := event.Body[1].(map[string]map[string]dbus.Variant); ok {
			data = interfaces[deviceInterface]
		}
	}
	if !strings.HasPrefix(string(path), string(adapter)+"/dev_") {
		return "", nil
	}
	manufacturers, _ := data["ManufacturerData"].Value().(map[uint16]dbus.Variant)
	payload, _ := manufacturers[Manufacturer].Value().([]byte)
	return string(path), payload
}
