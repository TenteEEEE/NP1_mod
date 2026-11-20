package pics

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"
)

const BrokerPath = "/pics/stock-advertisements"

var errStockStreamSilent = errors.New("純正Bluetooth受信の状態通知が途切れました")

type receiverMessage struct {
	Ready   bool   `json:"ready,omitempty"`
	Source  string `json:"source,omitempty"`
	Payload []byte `json:"payload,omitempty"`
	Error   string `json:"error,omitempty"`
}

// Scan consumes the stock Bluetooth stream from the credential-checked local
// broker. The unprivileged web process never connects to the vendor bus.
func Scan(client *http.Client) func(context.Context, func(), func(string, []byte)) error {
	return scanReceiver(client, 8*time.Second)
}

func scanReceiver(client *http.Client, inactivity time.Duration) func(context.Context, func(), func(string, []byte)) error {
	return func(ctx context.Context, ready func(), receive func(string, []byte)) error {
		stream, cancel := context.WithCancelCause(ctx)
		defer cancel(nil)
		timer := time.AfterFunc(inactivity, func() { cancel(errStockStreamSilent) })
		defer timer.Stop()
		req, err := http.NewRequestWithContext(stream, http.MethodGet, "http://broker"+BrokerPath, nil)
		if err != nil {
			return err
		}
		response, err := client.Do(req)
		if err != nil {
			if cause := context.Cause(stream); cause != nil {
				return cause
			}
			return errors.New("純正Bluetooth受信用の本体ブローカーに接続できません")
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			return fmt.Errorf("本体ブローカーの純正Bluetooth受信を利用できません（HTTP %d）", response.StatusCode)
		}
		scanner := bufio.NewScanner(response.Body)
		scanner.Buffer(make([]byte, 256), 1024)
		for scanner.Scan() {
			var message receiverMessage
			if err := json.Unmarshal(scanner.Bytes(), &message); err != nil {
				break
			}
			switch {
			case message.Error != "":
				return errors.New(message.Error)
			case message.Ready:
				timer.Reset(inactivity)
				ready()
			case message.Source != "" && len(message.Payload) >= 10 && len(message.Payload) <= 27:
				timer.Reset(inactivity)
				receive(message.Source, message.Payload)
			}
		}
		if cause := context.Cause(stream); cause != nil {
			return cause
		}
		return errors.New("純正Bluetoothからの受信が切断されました")
	}
}
