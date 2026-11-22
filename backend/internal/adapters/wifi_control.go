package adapters

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
)

const WiFiDirectory = "/overlay/owner-apps/np1-control/private/wifi"

// WPATransport is an internal controller dependency, not a public command API.
// Tests supply an in-memory supplicant; production uses the stock control socket.
type WPATransport interface {
	Request(context.Context, string) (string, error)
	Scan(context.Context) (string, error)
}
type vendorWPATransport struct{}

func (vendorWPATransport) Request(ctx context.Context, command string) (string, error) {
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	return vendorWPAContext(ctx, command)
}

type WiFiNetwork struct {
	ID       int    `json:"id"`
	SSID     string `json:"ssid"`
	Current  bool   `json:"current"`
	Disabled bool   `json:"disabled"`
	Managed  bool   `json:"managed"`
}
type WiFiConnection struct {
	State     string `json:"state"`
	SSID      string `json:"ssid"`
	NetworkID *int   `json:"networkID"`
	IPv4      string `json:"ipv4"`
}
type WiFiScanResult struct {
	SSID      string `json:"ssid"`
	BSSID     string `json:"bssid"`
	Frequency int    `json:"frequency"`
	SignalDBm int    `json:"signalDBm"`
	Security  string `json:"security"`
}
type WiFiScanView struct {
	ScanRequested    bool             `json:"scanRequested"`
	Results          []WiFiScanResult `json:"results"`
	ResultsAreCached bool             `json:"resultsAreCached"`
	ScannedAt        time.Time        `json:"scannedAt"`
}
type WiFiSavedProfile struct {
	ID                 string `json:"id"`
	SSID               string `json:"ssid"`
	Security           string `json:"security"`
	PasswordConfigured bool   `json:"passwordConfigured"`
	Password           string `json:"password"`
}
type WiFiTransaction struct {
	ID         string    `json:"id"`
	State      string    `json:"state"`
	ExpiresAt  time.Time `json:"expiresAt"`
	TargetSSID string    `json:"targetSSID"`
	CanConfirm bool      `json:"canConfirm"`
	Reason     string    `json:"reason,omitempty"`
}
type WiFiView struct {
	Status      WiFiConnection     `json:"status"`
	Networks    []WiFiNetwork      `json:"networks"`
	Saved       []WiFiSavedProfile `json:"saved"`
	Transaction *WiFiTransaction   `json:"transaction"`
	LastScan    *WiFiScanView      `json:"lastScan,omitempty"`
}
type WiFiNewConnection struct {
	SSID     string `json:"ssid"`
	Security string `json:"security"`
	Password string `json:"password,omitempty"`
}
type wifiProfile struct {
	ID       string `json:"id"`
	SSID     string `json:"ssid"`
	Security string `json:"security"`
	Password string `json:"password"`
}
type wifiPending struct {
	Public            WiFiTransaction `json:"public"`
	OldID             *int            `json:"oldID"`
	OldSSID           string          `json:"oldSSID"`
	TargetID          int             `json:"targetID"`
	CreatedID         *int            `json:"createdID"`
	Profile           *wifiProfile    `json:"profile,omitempty"`
	Enabled           []WiFiNetwork   `json:"enabled,omitempty"`
	TargetWasDisabled bool            `json:"targetWasDisabled,omitempty"`
	RestoreStationOff bool            `json:"restoreStationOff,omitempty"`
}
type WiFiController struct {
	mu        sync.Mutex
	transport WPATransport
	directory string
	pending   *wifiPending
	last      *WiFiTransaction
	lastScan  *WiFiScanView
	timer     *time.Timer
	timeout   time.Duration
}

func NewWiFiController(directory string, transport WPATransport) *WiFiController {
	if transport == nil {
		transport = vendorWPATransport{}
	}
	return &WiFiController{directory: directory, transport: transport, timeout: 90 * time.Second}
}

var wifiController = NewWiFiController(WiFiDirectory, nil)

func WiFiNetworks(ctx context.Context) (WiFiView, error) { return wifiController.List(ctx) }
func WiFiScan(ctx context.Context) (WiFiScanView, error) {
	return withUplinkWiFi(ctx, func(ctx context.Context, _ *uplinkWiFiUse) (WiFiScanView, error) {
		return wifiController.Scan(ctx)
	})
}
func WiFiConnectSaved(ctx context.Context, networkID int) (WiFiTransaction, error) {
	return connectWithUplink(ctx, func(ctx context.Context) (WiFiTransaction, error) {
		return wifiController.ConnectSaved(ctx, networkID)
	})
}
func WiFiConnectNew(ctx context.Context, change WiFiNewConnection) (WiFiTransaction, error) {
	if !validWiFiConnection(change) {
		return WiFiTransaction{}, errors.New("WIFI_CONNECTION_INVALID")
	}
	return connectWithUplink(ctx, func(ctx context.Context) (WiFiTransaction, error) {
		return wifiController.ConnectNew(ctx, change)
	})
}
func WiFiConnectProfile(ctx context.Context, profileID string) (WiFiTransaction, error) {
	return connectWithUplink(ctx, func(ctx context.Context) (WiFiTransaction, error) {
		return wifiController.ConnectProfile(ctx, profileID)
	})
}
func WiFiConfirm(ctx context.Context, id string) (WiFiTransaction, error) {
	return finishWiFiUse(ctx, id, true)
}
func WiFiRollback(ctx context.Context, id string) (WiFiTransaction, error) {
	return finishWiFiUse(ctx, id, false)
}

// RecoverWiFi rolls back an interrupted transaction before allowing mutations.
func RecoverWiFi(ctx context.Context) error {
	wait, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	unlock, err := lockUplinkWiFi(wait)
	if err != nil {
		return err
	}
	defer unlock()
	return wifiController.Recover(ctx)
}

func (c *WiFiController) usesStockStation() bool {
	_, stock := c.transport.(vendorWPATransport)
	return stock
}

func (c *WiFiController) request(ctx context.Context, command string) (string, error) {
	raw, e := c.transport.Request(ctx, command)
	if e != nil {
		return "", errors.New("WIFI_CONTROL_UNAVAILABLE")
	}
	if len(raw) > 32768 {
		return "", errors.New("WIFI_RESPONSE_TOO_LARGE")
	}
	raw = strings.TrimRight(raw, "\r\n")
	if strings.HasPrefix(raw, "FAIL") || strings.HasPrefix(raw, "UNKNOWN COMMAND") {
		return "", errors.New("WIFI_COMMAND_REJECTED")
	}
	return raw, nil
}
func (c *WiFiController) ok(ctx context.Context, command string) error {
	raw, e := c.request(ctx, command)
	if e != nil {
		return e
	}
	if raw != "OK" {
		return errors.New("WIFI_RESPONSE_INVALID")
	}
	return nil
}
func wifiID(raw string) (int, error) {
	if len(raw) == 0 || len(raw) > 9 {
		return 0, errors.New("WIFI_NETWORK_ID_INVALID")
	}
	for _, r := range raw {
		if r < '0' || r > '9' {
			return 0, errors.New("WIFI_NETWORK_ID_INVALID")
		}
	}
	value, e := strconv.Atoi(raw)
	if e != nil || value > 1000000 {
		return 0, errors.New("WIFI_NETWORK_ID_INVALID")
	}
	return value, nil
}
func wifiUUID() (string, error) {
	var b [16]byte
	if _, e := rand.Read(b[:]); e != nil {
		return "", e
	}
	b[6] = (b[6] & 15) | 64
	b[8] = (b[8] & 63) | 128
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:]), nil
}
func wifiSSID(value string) bool {
	if !utf8.ValidString(value) || len(value) < 1 || len(value) > 32 {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}
func wifiPassword(value string) bool {
	if len(value) == 64 {
		_, e := hex.DecodeString(value)
		return e == nil
	}
	if len(value) < 8 || len(value) > 63 {
		return false
	}
	for _, b := range []byte(value) {
		if b < 32 || b > 126 {
			return false
		}
	}
	return true
}
func decodeWPASSID(value string) (string, error) {
	var out []byte
	for i := 0; i < len(value); i++ {
		if value[i] != '\\' {
			out = append(out, value[i])
			continue
		}
		i++
		if i >= len(value) {
			return "", errors.New("WIFI_SSID_ENCODING_INVALID")
		}
		switch value[i] {
		case 'x':
			if i+2 >= len(value) {
				return "", errors.New("WIFI_SSID_ENCODING_INVALID")
			}
			part, e := hex.DecodeString(value[i+1 : i+3])
			if e != nil {
				return "", errors.New("WIFI_SSID_ENCODING_INVALID")
			}
			out = append(out, part[0])
			i += 2
		case '\\', '"':
			out = append(out, value[i])
		case 'n':
			out = append(out, '\n')
		case 'r':
			out = append(out, '\r')
		case 't':
			out = append(out, '\t')
		default:
			return "", errors.New("WIFI_SSID_ENCODING_INVALID")
		}
	}
	if !utf8.Valid(out) {
		return "", errors.New("WIFI_SSID_ENCODING_INVALID")
	}
	return string(out), nil
}
func parseWiFiStatus(raw string) (WiFiConnection, error) {
	out := WiFiConnection{}
	for _, line := range strings.Split(raw, "\n") {
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		switch parts[0] {
		case "wpa_state":
			out.State = parts[1]
		case "ssid":
			value, e := decodeWPASSID(parts[1])
			if e != nil {
				return out, e
			}
			out.SSID = value
		case "id":
			value, e := wifiID(parts[1])
			if e != nil {
				return out, e
			}
			out.NetworkID = &value
		case "ip_address":
			out.IPv4 = parts[1]
		}
	}
	if out.State == "" {
		return out, errors.New("WIFI_STATUS_INVALID")
	}
	return out, nil
}
func parseWiFiNetworks(raw string) ([]WiFiNetwork, error) {
	lines := strings.Split(raw, "\n")
	if len(lines) == 0 || (lines[0] != "network id / ssid / bssid / flags" && lines[0] != "network id\tssid\tbssid\tflags") {
		return nil, errors.New("WIFI_NETWORK_LIST_INVALID")
	}
	out := []WiFiNetwork{}
	seen := map[int]bool{}
	for _, line := range lines[1:] {
		if line == "" {
			continue
		}
		p := strings.Split(line, "\t")
		if len(p) != 4 || len(out) >= 128 {
			return nil, errors.New("WIFI_NETWORK_LIST_INVALID")
		}
		id, e := wifiID(p[0])
		if e != nil || seen[id] {
			return nil, errors.New("WIFI_NETWORK_LIST_INVALID")
		}
		ssid, e := decodeWPASSID(p[1])
		if e != nil {
			return nil, e
		}
		seen[id] = true
		out = append(out, WiFiNetwork{ID: id, SSID: ssid, Current: strings.Contains(p[3], "[CURRENT]"), Disabled: strings.Contains(p[3], "[DISABLED]")})
	}
	return out, nil
}
func (c *WiFiController) connection(ctx context.Context) (WiFiConnection, error) {
	raw, e := c.request(ctx, "STATUS")
	if e != nil {
		return WiFiConnection{}, e
	}
	connection, err := parseWiFiStatus(raw)
	if err == nil && connection.IPv4 == "" && c.usesStockStation() {
		connection.IPv4 = wifiLeaseAddress(raw, connection)
	}
	return connection, err
}
func (c *WiFiController) networks(ctx context.Context) ([]WiFiNetwork, error) {
	raw, e := c.request(ctx, "LIST_NETWORKS")
	if e != nil {
		return nil, e
	}
	return parseWiFiNetworks(raw)
}
func (c *WiFiController) List(ctx context.Context) (WiFiView, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e := c.recoverIfNeeded(ctx); e != nil {
		return WiFiView{}, e
	}
	profiles, e := c.loadProfiles()
	if e != nil {
		return WiFiView{}, e
	}
	saved := []WiFiSavedProfile{}
	for _, p := range profiles {
		saved = append(saved, WiFiSavedProfile{ID: p.ID, SSID: p.SSID, Security: p.Security, PasswordConfigured: p.Password != "", Password: p.Password})
	}
	if c.lastScan != nil && time.Since(c.lastScan.ScannedAt) > 2*time.Minute {
		c.lastScan = nil
	}
	if c.usesStockStation() && c.pending == nil {
		if on, err := stockSTAOn(); err == nil && !on {
			return WiFiView{WiFiConnection{State: "STA_OFF"}, []WiFiNetwork{}, saved, c.last, c.lastScan}, nil
		}
	}
	connection, e := c.connection(ctx)
	if e != nil {
		if c.usesStockStation() && c.pending == nil && e.Error() == "WIFI_CONTROL_UNAVAILABLE" {
			return WiFiView{WiFiConnection{State: "STA_UNAVAILABLE"}, []WiFiNetwork{}, saved, c.last, c.lastScan}, nil
		}
		return WiFiView{}, e
	}
	networks, e := c.networks(ctx)
	if e != nil {
		return WiFiView{}, e
	}
	for i := range networks {
		if c.pending != nil && c.pending.CreatedID != nil && networks[i].ID == *c.pending.CreatedID {
			networks[i].Managed = true
		}
	}
	transaction := c.last
	if c.pending != nil {
		view := c.pending.Public
		view.CanConfirm = c.targetConnected(connection, c.pending)
		transaction = &view
	}
	return WiFiView{connection, networks, saved, transaction, c.lastScan}, nil
}
func (c *WiFiController) Scan(ctx context.Context) (WiFiScanView, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	raw, e := c.transport.Scan(ctx)
	if e != nil {
		return WiFiScanView{}, e
	}
	results, e := parseWiFiScan(raw)
	if e != nil {
		return WiFiScanView{}, e
	}
	view := WiFiScanView{ScanRequested: true, Results: results, ScannedAt: time.Now().UTC()}
	cached := view
	cached.ResultsAreCached = true
	c.lastScan = &cached
	return view, nil
}

func parseWiFiScan(raw string) ([]WiFiScanResult, error) {
	lines := strings.Split(raw, "\n")
	if len(lines) == 0 || !strings.HasPrefix(lines[0], "bssid / frequency") {
		return nil, errors.New("WIFI_SCAN_INVALID")
	}
	out := []WiFiScanResult{}
	for _, line := range lines[1:] {
		if line == "" {
			continue
		}
		p := strings.SplitN(line, "\t", 5)
		if len(p) != 5 || len(out) >= 256 {
			continue
		}
		frequency, e := strconv.Atoi(p[1])
		if e != nil || frequency < 2000 || frequency > 7200 {
			continue
		}
		signal, e := strconv.Atoi(p[2])
		if e != nil || signal < -150 || signal > 0 {
			continue
		}
		ssid, e := decodeWPASSID(p[4])
		if e != nil || !wifiSSID(ssid) {
			continue
		}
		security := "unsupported"
		switch {
		case strings.Contains(p[3], "WPA2-PSK"):
			security = "wpa2"
		case !strings.Contains(p[3], "WPA") && !strings.Contains(p[3], "WEP") && !strings.Contains(p[3], "SAE") && !strings.Contains(p[3], "EAP") && !strings.Contains(p[3], "OWE"):
			security = "open"
		}
		out = append(out, WiFiScanResult{ssid, p[0], frequency, signal, security})
	}
	return out, nil
}

func (c *WiFiController) hasPending() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.pending != nil
}

func targetWiFiConnected(connection WiFiConnection, pending *wifiPending) bool {
	ip, err := netip.ParseAddr(connection.IPv4)
	return connection.State == "COMPLETED" && connection.NetworkID != nil && *connection.NetworkID == pending.TargetID &&
		connection.SSID == pending.Public.TargetSSID && err == nil && ip.Is4() && ip.IsGlobalUnicast() && !ip.IsLinkLocalUnicast()
}

func (c *WiFiController) targetConnected(connection WiFiConnection, pending *wifiPending) bool {
	if !targetWiFiConnected(connection, pending) {
		return false
	}
	if !c.usesStockStation() {
		return true
	}
	iface, err := net.InterfaceByName("wlan0")
	if err != nil {
		return false
	}
	address, _ := netip.ParseAddr(connection.IPv4)
	return stationHasIPv4(iface, address)
}

func stationHasIPv4(iface *net.Interface, expected netip.Addr) bool {
	if !expected.Is4() || !expected.IsGlobalUnicast() || expected.IsLinkLocalUnicast() {
		return false
	}
	addresses, err := iface.Addrs()
	if err != nil {
		return false
	}
	for _, address := range addresses {
		if prefix, e := netip.ParsePrefix(address.String()); e == nil && prefix.Addr() == expected {
			return true
		}
	}
	return false
}

func (c *WiFiController) begin(ctx context.Context, target int, ssid string, created *int, profile *wifiProfile) (WiFiTransaction, error) {
	connection, e := c.connection(ctx)
	if e != nil {
		return WiFiTransaction{}, e
	}
	id, e := wifiUUID()
	if e != nil {
		return WiFiTransaction{}, errors.New("WIFI_RANDOM_UNAVAILABLE")
	}
	pending := &wifiPending{Public: WiFiTransaction{ID: id, State: "pending", ExpiresAt: time.Now().Add(c.timeout), TargetSSID: ssid}, OldID: connection.NetworkID, TargetID: target, CreatedID: created, Profile: profile}
	if connection.State != "COMPLETED" {
		pending.OldID = nil
	}
	if c.usesStockStation() {
		use, err := readUplinkWiFiUse()
		if err != nil {
			return WiFiTransaction{}, errors.New("WIFI_SETUP_UNAVAILABLE")
		}
		pending.RestoreStationOff = use.RestoreOff
	}
	networks, e := c.networks(ctx)
	if e != nil {
		return WiFiTransaction{}, e
	}
	for _, network := range networks {
		if network.ID == target {
			pending.TargetWasDisabled = network.Disabled && created == nil
		} else if !network.Disabled {
			pending.Enabled = append(pending.Enabled, network)
		}
	}
	if pending.OldID != nil {
		found := false
		for _, network := range networks {
			if network.ID == *pending.OldID {
				pending.OldSSID = network.SSID
				found = true
				break
			}
		}
		if !found {
			return WiFiTransaction{}, errors.New("WIFI_PREVIOUS_NETWORK_UNAVAILABLE")
		}
	}
	if e = c.savePending(pending); e != nil {
		return WiFiTransaction{}, e
	}
	c.pending = pending
	if e = c.ok(ctx, "SELECT_NETWORK "+strconv.Itoa(target)); e != nil {
		_, rollback := c.rollbackLocked("connect-failed")
		if rollback != nil {
			return WiFiTransaction{}, rollback
		}
		return WiFiTransaction{}, e
	}
	c.timer = time.AfterFunc(c.timeout, func() {
		c.mu.Lock()
		defer c.mu.Unlock()
		if c.pending != nil && c.pending.Public.ID == id {
			_, _ = c.rollbackLocked("confirmation-timeout")
		}
	})
	return pending.Public, nil
}
func (c *WiFiController) ready(ctx context.Context) error {
	if e := c.recoverIfNeeded(ctx); e != nil {
		return e
	}
	if c.pending != nil {
		return errors.New("WIFI_TRANSACTION_PENDING")
	}
	return nil
}
func (c *WiFiController) ConnectSaved(ctx context.Context, id int) (WiFiTransaction, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if id < 0 || id > 1000000 {
		return WiFiTransaction{}, errors.New("WIFI_NETWORK_ID_INVALID")
	}
	if e := c.ready(ctx); e != nil {
		return WiFiTransaction{}, e
	}
	networks, e := c.networks(ctx)
	if e != nil {
		return WiFiTransaction{}, e
	}
	for _, network := range networks {
		if network.ID == id {
			return c.begin(ctx, id, network.SSID, nil, nil)
		}
	}
	return WiFiTransaction{}, errors.New("WIFI_NETWORK_NOT_FOUND")
}
func (c *WiFiController) ConnectNew(ctx context.Context, change WiFiNewConnection) (WiFiTransaction, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.connectNewLocked(ctx, change, "")
}
func (c *WiFiController) ConnectProfile(ctx context.Context, profileID string) (WiFiTransaction, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	profiles, e := c.loadProfiles()
	if e != nil {
		return WiFiTransaction{}, e
	}
	for _, p := range profiles {
		if p.ID == profileID {
			if e := c.ready(ctx); e != nil {
				return WiFiTransaction{}, e
			}
			networks, e := c.networks(ctx)
			if e != nil {
				return WiFiTransaction{}, e
			}
			network, ok, e := c.profileNetwork(ctx, networks, p)
			if e != nil {
				return WiFiTransaction{}, e
			}
			if ok {
				return c.begin(ctx, network.ID, p.SSID, nil, &p)
			}
			return c.connectNewLocked(ctx, WiFiNewConnection{p.SSID, p.Security, p.Password}, p.ID)
		}
	}
	return WiFiTransaction{}, errors.New("WIFI_SAVED_PROFILE_NOT_FOUND")
}
func (c *WiFiController) connectNewLocked(ctx context.Context, change WiFiNewConnection, profileID string) (WiFiTransaction, error) {
	if !validWiFiConnection(change) {
		return WiFiTransaction{}, errors.New("WIFI_CONNECTION_INVALID")
	}
	if e := c.ready(ctx); e != nil {
		return WiFiTransaction{}, e
	}
	if profileID == "" {
		uuid, e := wifiUUID()
		if e != nil {
			return WiFiTransaction{}, errors.New("WIFI_RANDOM_UNAVAILABLE")
		}
		profileID = "ncc-" + uuid
	}
	profile := wifiProfile{ID: profileID, SSID: change.SSID, Security: change.Security, Password: change.Password}
	id, e := c.addProfileNetwork(ctx, profile)
	if e != nil {
		return WiFiTransaction{}, e
	}
	transaction, e := c.begin(ctx, id, change.SSID, &id, &profile)
	if e != nil && c.pending == nil {
		c.removeProfileNetwork(id, profile.ID)
	}
	return transaction, e
}

func validWiFiConnection(change WiFiNewConnection) bool {
	return wifiSSID(change.SSID) && (change.Security == "open" && change.Password == "" || change.Security == "wpa2" && wifiPassword(change.Password))
}

func (c *WiFiController) removeProfileNetwork(id int, profileID string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	owner, e := c.request(ctx, "GET_NETWORK "+strconv.Itoa(id)+" id_str")
	if e == nil && owner == strconv.Quote(profileID) {
		_ = c.ok(ctx, "REMOVE_NETWORK "+strconv.Itoa(id))
	}
}

func (c *WiFiController) addProfileNetwork(ctx context.Context, profile wifiProfile) (int, error) {
	raw, e := c.request(ctx, "ADD_NETWORK")
	if e != nil {
		return 0, e
	}
	id, e := wifiID(raw)
	if e != nil {
		return 0, e
	}
	if e = c.ok(ctx, "SET_NETWORK "+strconv.Itoa(id)+" id_str "+strconv.Quote(profile.ID)); e != nil {
		background, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		_ = c.ok(background, "REMOVE_NETWORK "+strconv.Itoa(id)) // ID is the freshly returned ADD_NETWORK object.
		return 0, e
	}
	commands := []string{"ssid " + hex.EncodeToString([]byte(profile.SSID)), "scan_ssid 1"}
	if profile.Security == "open" {
		commands = append(commands, "key_mgmt NONE")
	} else {
		// Stock wpa_config_parse_psk copies bytes between the first and last quote;
		// it does not unescape Go/JSON backslashes. Printable ASCII was validated.
		psk := "\"" + profile.Password + "\""
		if len(profile.Password) == 64 {
			psk = profile.Password
		}
		commands = append(commands, "key_mgmt WPA-PSK", "proto RSN", "psk "+psk)
	}
	for _, command := range commands {
		if e = c.ok(ctx, "SET_NETWORK "+strconv.Itoa(id)+" "+command); e != nil {
			c.removeProfileNetwork(id, profile.ID)
			return 0, e
		}
	}
	return id, nil
}
func (c *WiFiController) Confirm(ctx context.Context, id string) (WiFiTransaction, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e := c.recoverIfNeeded(ctx); e != nil {
		return WiFiTransaction{}, e
	}
	if c.pending == nil || c.pending.Public.ID != id {
		return WiFiTransaction{}, errors.New("WIFI_TRANSACTION_NOT_FOUND")
	}
	if !time.Now().Before(c.pending.Public.ExpiresAt) {
		return c.rollbackLocked("confirmation-timeout")
	}
	connection, e := c.connection(ctx)
	if e != nil {
		return WiFiTransaction{}, e
	}
	if !c.targetConnected(connection, c.pending) {
		return WiFiTransaction{}, errors.New("WIFI_TARGET_NOT_CONNECTED")
	}
	if c.pending.Profile != nil {
		owner, e := c.request(ctx, "GET_NETWORK "+strconv.Itoa(c.pending.TargetID)+" id_str")
		if e != nil || owner != strconv.Quote(c.pending.Profile.ID) {
			return WiFiTransaction{}, errors.New("WIFI_TARGET_OWNERSHIP_MISMATCH")
		}
		if e := c.setProfileOption(ctx, c.pending.TargetID, "priority", strconv.Itoa(wifiTrialPriority)); e != nil {
			return WiFiTransaction{}, e
		}
	}
	if e = c.restoreEnabled(ctx, c.pending); e != nil {
		return WiFiTransaction{}, e
	}
	if c.pending.Profile != nil {
		if e = c.storeProfile(*c.pending.Profile); e != nil {
			return WiFiTransaction{}, e
		}
	}
	result := c.pending.Public
	result.State = "confirmed"
	result.CanConfirm = false
	if e = c.clearPending(); e != nil {
		return WiFiTransaction{}, e
	}
	if c.timer != nil {
		c.timer.Stop()
	}
	c.pending = nil
	c.last = &result
	return result, nil
}
func (c *WiFiController) Rollback(_ context.Context, id string) (WiFiTransaction, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e := c.recoverIfNeeded(context.Background()); e != nil {
		return WiFiTransaction{}, e
	}
	if c.pending == nil || c.pending.Public.ID != id {
		return WiFiTransaction{}, errors.New("WIFI_TRANSACTION_NOT_FOUND")
	}
	return c.rollbackLocked("user-request")
}
func (c *WiFiController) rollbackLocked(reason string) (WiFiTransaction, error) {
	if c.pending == nil {
		return WiFiTransaction{}, errors.New("WIFI_TRANSACTION_NOT_FOUND")
	}
	p := c.pending
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	if p.RestoreStationOff && c.usesStockStation() {
		// After an interrupted setup, the supervisor may already have
		// restored OFF and the in-memory supplicant network has disappeared.
		if on, err := stockSTAOn(); err == nil && !on {
			if _, err := c.transport.Request(ctx, "PING"); errors.Is(err, os.ErrNotExist) {
				return c.completeRollback(p, reason)
			}
		}
	}
	command := "DISCONNECT"
	if p.OldID != nil {
		networks, e := c.networks(ctx)
		if e != nil {
			return WiFiTransaction{}, errors.New("WIFI_ROLLBACK_RESTORE_FAILED")
		}
		found := false
		for _, network := range networks {
			if network.ID == *p.OldID && network.SSID == p.OldSSID {
				found = true
				break
			}
		}
		if !found {
			return WiFiTransaction{}, errors.New("WIFI_ROLLBACK_PREVIOUS_ID_CHANGED")
		}
		command = "SELECT_NETWORK " + strconv.Itoa(*p.OldID)
	}
	if e := c.ok(ctx, command); e != nil {
		return WiFiTransaction{}, errors.New("WIFI_ROLLBACK_RESTORE_FAILED")
	}
	if p.TargetWasDisabled {
		networks, err := c.networks(ctx)
		if err != nil {
			return WiFiTransaction{}, errors.New("WIFI_ROLLBACK_RESTORE_FAILED")
		}
		for _, network := range networks {
			if network.ID == p.TargetID && network.SSID == p.Public.TargetSSID && !network.Disabled {
				if err := c.ok(ctx, "DISABLE_NETWORK "+strconv.Itoa(network.ID)); err != nil {
					return WiFiTransaction{}, errors.New("WIFI_ROLLBACK_RESTORE_FAILED")
				}
				break
			}
		}
	}
	if p.CreatedID != nil {
		owner, e := c.request(ctx, "GET_NETWORK "+strconv.Itoa(*p.CreatedID)+" id_str")
		absent := false
		if e != nil {
			networks, listErr := c.networks(ctx)
			if listErr != nil {
				return WiFiTransaction{}, errors.New("WIFI_ROLLBACK_OWNERSHIP_MISMATCH")
			}
			absent = true
			for _, network := range networks {
				if network.ID == *p.CreatedID {
					absent = false
				}
			}
		}
		if !absent {
			if e != nil || p.Profile == nil || owner != strconv.Quote(p.Profile.ID) {
				return WiFiTransaction{}, errors.New("WIFI_ROLLBACK_OWNERSHIP_MISMATCH")
			}
			if e = c.ok(ctx, "REMOVE_NETWORK "+strconv.Itoa(*p.CreatedID)); e != nil {
				return WiFiTransaction{}, errors.New("WIFI_ROLLBACK_REMOVE_FAILED")
			}
		}
	}
	verified := false
	until := time.Now().Add(8 * time.Second)
	for time.Now().Before(until) && ctx.Err() == nil {
		connection, e := c.connection(ctx)
		if e == nil {
			if p.OldID == nil {
				verified = connection.NetworkID == nil && (connection.State == "DISCONNECTED" || connection.State == "INACTIVE")
			} else {
				verified = connection.State == "COMPLETED" && connection.NetworkID != nil && *connection.NetworkID == *p.OldID && connection.SSID == p.OldSSID
			}
		}
		if verified {
			break
		}
		select {
		case <-ctx.Done():
		case <-time.After(200 * time.Millisecond):
		}
	}
	if !verified {
		return WiFiTransaction{}, errors.New("WIFI_ROLLBACK_CONNECTION_UNVERIFIED")
	}
	if e := c.restoreEnabled(ctx, p); e != nil {
		return WiFiTransaction{}, e
	}
	return c.completeRollback(p, reason)
}

func (c *WiFiController) completeRollback(p *wifiPending, reason string) (WiFiTransaction, error) {
	result := p.Public
	result.State = "rolled-back"
	result.CanConfirm = false
	result.Reason = reason
	if e := c.clearPending(); e != nil {
		return WiFiTransaction{}, e
	}
	if c.timer != nil {
		c.timer.Stop()
	}
	c.pending = nil
	c.last = &result
	return result, nil
}

func (c *WiFiController) restoreEnabled(ctx context.Context, p *wifiPending) error {
	if len(p.Enabled) == 0 {
		return nil
	}
	networks, err := c.networks(ctx)
	if err != nil {
		return errors.New("WIFI_RESTORE_NETWORKS_UNAVAILABLE")
	}
	for _, original := range p.Enabled {
		for _, current := range networks {
			if current.ID == original.ID && current.SSID == original.SSID && current.Disabled {
				if err := c.ok(ctx, "ENABLE_NETWORK "+strconv.Itoa(current.ID)); err != nil {
					return errors.New("WIFI_RESTORE_NETWORKS_UNAVAILABLE")
				}
				break
			}
		}
	}
	return nil
}
