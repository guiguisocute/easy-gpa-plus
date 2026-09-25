package netguard

import (
	"context"
	"crypto/tls"
	"net"
	"net/netip"
	"time"
)

// BackupAddresses keeps fake-IP proxy DNS out of the backup connection path.
// Only synthetic benchmark addresses trigger a second lookup, over authenticated
// DNS-over-TLS to a fixed public resolver. Callers still check every returned IP.
// This does not allow private addresses or send S3 credentials to the resolver.
func BackupAddresses(ctx context.Context, host string) ([]net.IPAddr, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	addresses, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}
	if !hasFakeAddress(addresses) {
		return addresses, nil
	}
	resolver := &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, _, _ string) (net.Conn, error) {
			dialer := &tls.Dialer{
				NetDialer: &net.Dialer{Timeout: 5 * time.Second},
				Config:    &tls.Config{ServerName: "cloudflare-dns.com", MinVersion: tls.VersionTLS12},
			}
			return dialer.DialContext(ctx, "tcp", "1.1.1.1:853")
		},
	}
	return resolver.LookupIPAddr(ctx, host)
}

func hasFakeAddress(addresses []net.IPAddr) bool {
	rangeIP := netip.MustParsePrefix("198.18.0.0/15")
	for _, candidate := range addresses {
		if ip, ok := netip.AddrFromSlice(candidate.IP); ok && rangeIP.Contains(ip.Unmap()) {
			return true
		}
	}
	return false
}
