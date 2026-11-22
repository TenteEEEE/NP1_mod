package adapters

import (
	"testing"
	"time"
)

func TestWiFiDHCPLeaseValidation(t *testing.T) {
	const offered = "NCC_LEASE=10.247.63.21|255.255.255.0|10.247.63.1|120\n"
	lease, err := parseWiFiDHCPLease("vendor line\n" + offered)
	if err != nil || lease.address.String() != "10.247.63.21/24" || lease.gateway.String() != "10.247.63.1" || lease.valid != 2*time.Minute {
		t.Fatalf("valid lease: %+v %v", lease, err)
	}
	for _, invalid := range []string{
		"", offered + offered,
		"NCC_LEASE=10.247.63.21|255.0.255.0|10.247.63.1|120",
		"NCC_LEASE=10.247.63.21|255.255.255.0|10.247.64.1|120",
		"NCC_LEASE=10.247.63.255|255.255.255.0|10.247.63.1|120",
		"NCC_LEASE=10.247.63.21|255.255.255.0|10.247.63.1 10.247.63.2|120",
		"NCC_LEASE=10.247.63.21|255.255.255.0|10.247.63.21|120",
		"NCC_LEASE=10.247.63.21|255.255.255.0|10.247.63.1|1",
		"NCC_LEASE=169.254.0.2|255.255.255.0|169.254.0.1|120",
	} {
		if _, err := parseWiFiDHCPLease(invalid); err == nil {
			t.Fatalf("accepted invalid lease %q", invalid)
		}
	}
}
