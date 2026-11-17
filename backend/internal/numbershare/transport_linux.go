//go:build linux

package numbershare

import (
	"bufio"
	"context"
	"errors"
	"net"
	"net/http"
	"os"
	"strings"
	"syscall"
	"time"
)

const bindToDevice = 25

type cellularIdentity struct {
	iface  string
	source net.IP
}

func newHTTPClient() *http.Client {
	fallback := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	return &http.Client{Timeout: 30 * time.Second, Transport: &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           preferCellularDial(fallback),
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 20 * time.Second,
	}}
}

func preferCellularDial(fallback *net.Dialer) func(context.Context, string, string) (net.Conn, error) {
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		id, err := currentCellularIdentity()
		if err == nil {
			if connection, dialErr := dialCellular(ctx, network, address, id); dialErr == nil {
				return connection, nil
			}
		}
		return fallback.DialContext(ctx, network, address)
	}
}

func currentCellularIdentity() (cellularIdentity, error) {
	for _, name := range []string{"usb0.1", "usb0", "usb0.2"} {
		iface, err := net.InterfaceByName(name)
		if err != nil || iface.Flags&net.FlagUp == 0 {
			continue
		}
		addresses, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, address := range addresses {
			ip, _, err := net.ParseCIDR(address.String())
			if err == nil && ip.To4() != nil && !ip.IsUnspecified() {
				return cellularIdentity{iface: name, source: ip.To4()}, nil
			}
		}
	}
	return cellularIdentity{}, errors.New("cellular-interface-unavailable")
}

func resolverServers() []string {
	f, err := os.Open("/etc/resolv.conf")
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) != 2 || fields[0] != "nameserver" {
			continue
		}
		ip := net.ParseIP(fields[1])
		if ip == nil || ip.To4() == nil {
			continue
		}
		out = append(out, net.JoinHostPort(ip.String(), "53"))
		if len(out) == 4 {
			break
		}
	}
	return out
}

func socketControl(iface string) func(string, string, syscall.RawConn) error {
	return func(_, _ string, raw syscall.RawConn) error {
		var bindErr error
		if err := raw.Control(func(fd uintptr) {
			bindErr = syscall.SetsockoptString(int(fd), syscall.SOL_SOCKET, bindToDevice, iface)
		}); err != nil {
			return err
		}
		return bindErr
	}
}

func resolveCellular(ctx context.Context, id cellularIdentity, host string) ([]net.IPAddr, error) {
	if literal := net.ParseIP(host); literal != nil {
		return []net.IPAddr{{IP: literal}}, nil
	}
	var last error
	for _, server := range resolverServers() {
		server := server
		resolver := &net.Resolver{PreferGo: true, StrictErrors: true, Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
			if network != "udp" && network != "udp4" {
				return nil, errors.New("dns-transport-unsupported")
			}
			dialer := net.Dialer{Timeout: 4 * time.Second, LocalAddr: &net.UDPAddr{IP: id.source}, Control: socketControl(id.iface)}
			return dialer.DialContext(ctx, "udp4", server)
		}}
		addresses, err := resolver.LookupIPAddr(ctx, host)
		if err == nil && len(addresses) > 0 {
			return addresses, nil
		}
		last = err
		if ctx.Err() != nil {
			break
		}
	}
	if last == nil {
		last = errors.New("dns-unavailable")
	}
	return nil, last
}
func dialCellular(ctx context.Context, network, address string, id cellularIdentity) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	addresses, err := resolveCellular(ctx, id, host)
	if err != nil {
		return nil, err
	}
	dialer := net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second, LocalAddr: &net.TCPAddr{IP: id.source}, Control: socketControl(id.iface)}
	var last error
	for _, candidate := range addresses {
		if candidate.IP.To4() == nil {
			continue
		}
		connection, dialErr := dialer.DialContext(ctx, "tcp4", net.JoinHostPort(candidate.IP.String(), port))
		if dialErr == nil {
			return connection, nil
		}
		last = dialErr
	}
	if last == nil {
		last = errors.New("cellular-address-unavailable")
	}
	return nil, last
}

