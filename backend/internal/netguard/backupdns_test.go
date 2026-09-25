package netguard

import (
	"net"
	"testing"
)

func TestBackupDNSFallbackOnlyForSyntheticAddresses(t *testing.T) {
	for _, test := range []struct {
		ip   string
		want bool
	}{
		{"198.18.0.1", true}, {"198.19.255.254", true},
		{"::ffff:198.18.0.1", true}, {"198.20.0.1", false},
		{"192.0.2.1", false}, {"127.0.0.1", false},
		{"169.254.169.254", false}, {"1.1.1.1", false},
	} {
		if got := hasFakeAddress([]net.IPAddr{{IP: net.ParseIP(test.ip)}}); got != test.want {
			t.Errorf("%s: fallback=%v, want %v", test.ip, got, test.want)
		}
	}
}
