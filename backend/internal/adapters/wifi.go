package adapters

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net"
	"os"
	"os/user"
	"strconv"
	"strings"
	"time"
)

const stockWPASocket = "/run/wifi/wpa_supplicant/wlan0"

// VendorStatus issues only STATUS to the vendor-owned supplicant; no scan/configuration or second daemon.
// Bind the reply socket in our writable runtime directory, not the read-only /tmp sandbox.
func VendorStatus() (string, error) {
	return vendorWPA("STATUS")
}

// vendorWPA is private. Only typed controller methods may build mutation commands;
// arbitrary caller-supplied command strings are never exposed through the API.
func vendorWPA(command string) (string, error) {
	return vendorWPAContext(context.Background(), command)
}

func vendorWPAContext(ctx context.Context, command string) (string, error) {
	socket, closeSocket, err := openWPASocket()
	if err != nil {
		return "", err
	}
	defer closeSocket()
	return exchangeWPA(ctx, socket, command)
}

func openWPASocket() (*net.UnixConn, func(), error) {
	b := make([]byte, 8)
	if _, e := rand.Read(b); e != nil {
		return nil, nil, e
	}
	local := "/run/np1-control/wpa-" + hex.EncodeToString(b)
	c, e := net.DialUnix("unixgram", &net.UnixAddr{Name: local, Net: "unixgram"}, &net.UnixAddr{Name: stockWPASocket, Net: "unixgram"})
	if e != nil {
		return nil, nil, e
	}
	closeSocket := func() {
		_ = c.Close()
		_ = os.Remove(local)
	}
	group, e := user.LookupGroup("NEventWatcherCom")
	if e != nil {
		closeSocket()
		return nil, nil, e
	}
	gid, e := strconv.Atoi(group.Gid)
	if e != nil {
		closeSocket()
		return nil, nil, e
	}
	if e = os.Chown(local, 0, gid); e != nil {
		closeSocket()
		return nil, nil, e
	}
	if e = os.Chmod(local, 0660); e != nil {
		closeSocket()
		return nil, nil, e
	}
	return c, closeSocket, nil
}

func exchangeWPA(ctx context.Context, c *net.UnixConn, command string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	deadline := time.Now().Add(2 * time.Second)
	if end, ok := ctx.Deadline(); ok && end.Before(deadline) {
		deadline = end
	}
	_ = c.SetDeadline(deadline)
	if _, e := c.Write([]byte(command)); e != nil {
		return "", e
	}
	data := make([]byte, 32769)
	n, e := c.Read(data)
	if n > 32768 {
		return "", errors.New("WIFI_RESPONSE_TOO_LARGE")
	}
	return string(data[:n]), e
}

// Subscribe before requesting the scan so a fast scan cannot finish before
// the listener is ready. Use a separate command socket for replies.
func (vendorWPATransport) Scan(ctx context.Context) (string, error) {
	events, closeEvents, err := openWPASocket()
	if err != nil {
		return "", errors.New("WIFI_CONTROL_UNAVAILABLE")
	}
	defer closeEvents()
	if reply, err := exchangeWPA(ctx, events, "ATTACH"); err != nil || strings.TrimSpace(reply) != "OK" {
		return "", errors.New("WIFI_CONTROL_UNAVAILABLE")
	}
	defer func() { _, _ = exchangeWPA(context.Background(), events, "DETACH") }()
	raw, err := vendorWPAContext(ctx, "SCAN")
	if err != nil {
		return "", errors.New("WIFI_CONTROL_UNAVAILABLE")
	}
	switch strings.TrimSpace(raw) {
	case "OK", "FAIL-BUSY":
	default:
		return "", errors.New("WIFI_SCAN_REJECTED")
	}
	deadline := time.Now().Add(15 * time.Second)
	if end, ok := ctx.Deadline(); ok && end.Before(deadline) {
		deadline = end
	}
	_ = events.SetReadDeadline(deadline)
	buf := make([]byte, 4096)
	for {
		n, err := events.Read(buf)
		if err != nil {
			return "", errors.New("WIFI_SCAN_TIMEOUT")
		}
		event := string(buf[:n])
		if _, name, ok := strings.Cut(event, ">"); ok {
			name, _, _ = strings.Cut(name, " ")
			switch name {
			case "CTRL-EVENT-SCAN-RESULTS":
				raw, err := vendorWPAContext(ctx, "SCAN_RESULTS")
				if err != nil {
					return "", errors.New("WIFI_CONTROL_UNAVAILABLE")
				}
				return raw, nil
			case "CTRL-EVENT-SCAN-FAILED":
				return "", errors.New("WIFI_SCAN_REJECTED")
			}
		}
	}
}
