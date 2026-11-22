package adapters

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/np1-control-center/ncc/internal/config"
)

const wifiDHCPEvent = UplinkRuntime + "/dhcp-event"
const wifiDHCPState = UplinkRuntime + "/wifi-lease.json"

type wifiDHCPLease struct {
	address netip.Prefix
	gateway netip.Addr
	valid   time.Duration
}

func parseWiFiDHCPLease(raw string) (wifiDHCPLease, error) {
	invalid := errors.New("UPLINK_DHCP_LEASE_INVALID")
	var result wifiDHCPLease
	for _, line := range strings.Split(raw, "\n") {
		data, ok := strings.CutPrefix(line, "NCC_LEASE=")
		if !ok {
			continue
		}
		fields := strings.Split(data, "|")
		if result.address.IsValid() || len(fields) != 4 {
			return wifiDHCPLease{}, invalid
		}
		address, addrErr := netip.ParseAddr(fields[0])
		mask, maskErr := netip.ParseAddr(fields[1])
		gateway, gwErr := netip.ParseAddr(strings.TrimSpace(fields[2]))
		seconds, durationErr := strconv.Atoi(fields[3])
		if addrErr != nil || maskErr != nil || gwErr != nil || durationErr != nil || !address.Is4() || !mask.Is4() || !gateway.Is4() ||
			!address.IsGlobalUnicast() || address.IsLinkLocalUnicast() || !gateway.IsGlobalUnicast() || address == gateway || seconds < 20 || seconds > 604800 {
			return wifiDHCPLease{}, invalid
		}
		maskBytes := mask.As4()
		bits, width := net.IPMask(maskBytes[:]).Size()
		if width != 32 || bits < 8 || bits > 30 {
			return wifiDHCPLease{}, invalid
		}
		prefix := netip.PrefixFrom(address, bits)
		if !prefix.Contains(gateway) || prefix.Masked().Addr() == address || prefix.Masked().Addr() == gateway {
			return wifiDHCPLease{}, invalid
		}
		if rangeTop := ipv4PrefixLast(prefix); address == rangeTop || gateway == rangeTop {
			return wifiDHCPLease{}, invalid
		}
		result = wifiDHCPLease{prefix, gateway, time.Duration(seconds) * time.Second}
	}
	if !result.address.IsValid() {
		return wifiDHCPLease{}, invalid
	}
	return result, nil
}

func ipv4PrefixLast(prefix netip.Prefix) netip.Addr {
	base := prefix.Masked().Addr().As4()
	for bit := prefix.Bits(); bit < 32; bit++ {
		base[bit/8] |= 1 << (7 - bit%8)
	}
	return netip.AddrFrom4(base)
}

type wifiDHCPStation struct {
	index       int
	id          int
	ssid, bssid string
	profile     string
}

type wifiDHCPProof struct {
	Index     int       `json:"index"`
	NetworkID int       `json:"networkID"`
	SSID      string    `json:"ssid"`
	BSSID     string    `json:"bssid"`
	Address   string    `json:"address"`
	ExpiresAt time.Time `json:"expiresAt"`
}

func wifiLeaseAddress(raw string, status WiFiConnection) string {
	if status.State != "COMPLETED" || status.NetworkID == nil {
		return ""
	}
	data, err := os.ReadFile(wifiDHCPState)
	if err != nil || len(data) > 1024 {
		return ""
	}
	var proof wifiDHCPProof
	if json.Unmarshal(data, &proof) != nil || proof.NetworkID != *status.NetworkID || proof.SSID != status.SSID ||
		proof.BSSID != apSharingStatusFields(raw)["bssid"] || !time.Now().Before(proof.ExpiresAt) {
		return ""
	}
	address, err := netip.ParseAddr(proof.Address)
	iface, ifaceErr := net.InterfaceByName("wlan0")
	if err != nil || ifaceErr != nil || iface.Index != proof.Index || !stationHasIPv4(iface, address) {
		return ""
	}
	return address.String()
}

func managedDHCPStation(ctx context.Context, controller *WiFiController) (wifiDHCPStation, bool) {
	if !wifiCarries() {
		return wifiDHCPStation{}, false
	}
	iface, err := net.InterfaceByName("wlan0")
	if err != nil {
		return wifiDHCPStation{}, false
	}
	raw, err := controller.request(ctx, "STATUS")
	if err != nil {
		return wifiDHCPStation{}, false
	}
	connection, err := parseWiFiStatus(raw)
	if err != nil || connection.State != "COMPLETED" || connection.NetworkID == nil {
		return wifiDHCPStation{}, false
	}
	bssid := apSharingStatusFields(raw)["bssid"]
	if _, err := net.ParseMAC(bssid); err != nil {
		return wifiDHCPStation{}, false
	}
	id := *connection.NetworkID
	owner, err := controller.request(ctx, "GET_NETWORK "+strconv.Itoa(id)+" id_str")
	if err != nil {
		return wifiDHCPStation{}, false
	}
	station := wifiDHCPStation{iface.Index, id, connection.SSID, bssid, owner}
	matches := func(profile wifiProfile) bool {
		return profile.SSID == station.ssid && strconv.Quote(profile.ID) == station.profile
	}
	var pending wifiPending
	if controller.readPrivate("pending.json", &pending) == nil && pending.TargetID == id && pending.Profile != nil && time.Now().Before(pending.Public.ExpiresAt) && matches(*pending.Profile) {
		return station, true
	}
	profiles, err := controller.loadProfiles()
	if err == nil {
		for _, profile := range profiles {
			if matches(profile) {
				return station, true
			}
		}
	}
	return station, false
}

type wifiDHCPManager struct {
	station wifiDHCPStation
	lease   wifiDHCPLease
	renewAt time.Time
	endAt   time.Time
	retryAt time.Time
}

func (manager *wifiDHCPManager) route(operation string) []string {
	return []string{"-4", "route", operation, "default", "via", manager.lease.gateway.String(), "dev", "wlan0", "proto", "186", "metric", "501"}
}

func (manager *wifiDHCPManager) release(ctx context.Context) {
	if manager.lease.address.IsValid() {
		_ = os.Remove(wifiDHCPState)
		if iface, err := net.InterfaceByName("wlan0"); err == nil && iface.Index == manager.station.index {
			_, _ = apCellularIPCommand(ctx, manager.route("del")...)
			if addresses, err := apCellularIPCommand(ctx, "-4", "-o", "addr", "show", "dev", "wlan0", "label", "wlan0:ncc"); err == nil && strings.Contains(" "+addresses+" ", " "+manager.lease.address.String()+" ") {
				_, _ = apCellularIPCommand(ctx, "-4", "addr", "del", manager.lease.address.String(), "dev", "wlan0")
			}
		}
	}
	*manager = wifiDHCPManager{}
}

func (manager *wifiDHCPManager) tick(ctx context.Context, on bool, controller *WiFiController) {
	if !on {
		manager.release(ctx)
		return
	}
	station, managed := managedDHCPStation(ctx, controller)
	if !wifiCarries() || station.index != 0 && manager.station.index != 0 && (station != manager.station || !managed) || !manager.endAt.IsZero() && !time.Now().Before(manager.endAt) {
		manager.release(ctx)
	}
	if !managed || time.Now().Before(manager.retryAt) || manager.lease.address.IsValid() && time.Now().Before(manager.renewAt) {
		return
	}
	delay := time.Now().Add(12 * time.Second)
	manager.retryAt = delay
	addresses, err := apCellularIPCommand(ctx, "-4", "-o", "addr", "show", "dev", "wlan0", "scope", "global")
	if err != nil || !manager.lease.address.IsValid() && addresses != "" {
		return
	}
	deadline, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()
	raw, err := exec.CommandContext(deadline, "/bin/busybox", "udhcpc", "-f", "-n", "-q", "-t", "3", "-T", "1", "-i", "wlan0", "-s", wifiDHCPEvent).Output()
	if err != nil || len(raw) > 1024 {
		return
	}
	lease, err := parseWiFiDHCPLease(string(raw))
	if err != nil || wifiDHCPOverlapsLocal(lease.address) {
		return
	}
	current, verified := managedDHCPStation(ctx, controller)
	if !verified || station != current {
		return
	}
	if manager.lease.address != lease.address || manager.lease.gateway != lease.gateway {
		manager.release(ctx)
		manager.retryAt = delay
		addresses, err := apCellularIPCommand(ctx, "-4", "-o", "addr", "show", "dev", "wlan0", "scope", "global")
		if err != nil || addresses != "" {
			return
		}
		if _, err := apCellularIPCommand(ctx, "-4", "addr", "add", lease.address.String(), "dev", "wlan0", "label", "wlan0:ncc"); err != nil {
			return
		}
		manager.station, manager.lease = station, lease
		if _, err := apCellularIPCommand(ctx, manager.route("add")...); err != nil {
			manager.release(ctx)
			return
		}
	}
	manager.renewAt, manager.endAt = time.Now().Add(lease.valid/2), time.Now().Add(lease.valid)
	proof := wifiDHCPProof{station.index, station.id, station.ssid, station.bssid, lease.address.Addr().String(), manager.endAt}
	if err := config.Atomic(wifiDHCPState, proof); err != nil {
		manager.release(ctx)
	}
}

func wifiDHCPOverlapsLocal(candidate netip.Prefix) bool {
	for _, name := range []string{"bridge0", apInterface} {
		iface, err := net.InterfaceByName(name)
		if err != nil {
			continue
		}
		addresses, err := iface.Addrs()
		if err != nil {
			return true
		}
		for _, address := range addresses {
			if prefix, err := netip.ParsePrefix(address.String()); err == nil && prefix.Overlaps(candidate) {
				return true
			}
		}
	}
	return false
}
