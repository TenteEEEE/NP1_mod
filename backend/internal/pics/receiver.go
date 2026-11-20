package pics

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
)

const BrokerPath = "/pics/stock-advertisements"

type receiverMessage struct {
	Ready   bool   `json:"ready,omitempty"`
	Source  string `json:"source,omitempty"`
	Payload []byte `json:"payload,omitempty"`
	Error   string `json:"error,omitempty"`
}

// Scan consumes the stock Bluetooth stream from the credential-checked local
// broker. The unprivileged web process never connects to the vendor bus.
func Scan(client *http.Client) func(context.Context, func(), func(string, []byte)) error {
	return func(ctx context.Context, ready func(), receive func(string, []byte)) error {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://broker"+BrokerPath, nil)
		if err != nil {
			return err
		}
		response, err := client.Do(req)
		if err != nil {
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
				ready()
			case message.Source != "" && len(message.Payload) >= 10 && len(message.Payload) <= 27:
				receive(message.Source, message.Payload)
			}
		}
		return errors.New("純正Bluetoothからの受信が切断されました")
	}
}
