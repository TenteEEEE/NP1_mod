package adapters

import (
	"context"
	"crypto/x509"
	"errors"
	"net"
	"net/netip"
	"time"
)

const uplinkWiFiCheckEvery = 20 * time.Second

type uplinkWiFiHealth struct {
	checked time.Time
	passed  bool
	misses  int
}

func (health *uplinkWiFiHealth) working(now time.Time, link bool, probe func() bool) bool {
	if !link {
		*health = uplinkWiFiHealth{}
		return false
	}
	if health.checked.IsZero() || now.Before(health.checked) || now.Sub(health.checked) >= uplinkWiFiCheckEvery {
		health.checked = now
		if probe() {
			health.passed, health.misses = true, 0
		} else {
			health.misses++
			if health.misses >= 2 {
				health.passed = false
			}
		}
	}
	return health.passed
}

func wifiInternetSnapshot(ctx context.Context) (apDNSIdentity, error) {
	upstream, err := readVerifiedSTAUpstream(ctx)
	if err != nil {
		return apDNSIdentity{}, err
	}
	route, err := apCellularIPCommand(ctx, "-4", "route", "get", "1.1.1.1")
	if err != nil {
		return apDNSIdentity{}, err
	}
	gateway, err := apParseUpstreamRoute(route, "wlan0", upstream.address.String())
	if err != nil || upstream.gateway.IsValid() && gateway != upstream.gateway.String() || !upstream.gateway.IsValid() && gateway != "" {
		return apDNSIdentity{}, errors.New("UPLINK_WIFI_ROUTE_UNVERIFIED")
	}
	iface, err := net.InterfaceByName("wlan0")
	if err != nil {
		return apDNSIdentity{}, err
	}
	return apDNSIdentity{iface: "wlan0", index: iface.Index, boot: read("/proc/sys/kernel/random/boot_id"),
		address: netip.PrefixFrom(upstream.address, upstream.prefix.Bits()), gateway: gateway, servers: upstream.dns}, nil
}

func probeWiFiInternet(ctx context.Context) bool {
	_, err := runBoundInternetProbe(ctx, cellularProbeDependencies{
		snapshot: wifiInternetSnapshot,
		resolve:  resolveCellularProbe,
		dial:     cellularProbeDialTCP,
		roots:    x509.SystemCertPool,
		now:      time.Now,
	}, "np1-origin-bound-wifi", func(id apDNSIdentity) bool {
		return id.iface == "wlan0" && id.index > 0 && id.boot != "" && id.address.IsValid() &&
			id.address.Addr().Is4() && id.address.Addr().IsGlobalUnicast() && !id.address.Addr().IsLinkLocalUnicast() && len(id.servers) > 0
	})
	return err == nil
}
