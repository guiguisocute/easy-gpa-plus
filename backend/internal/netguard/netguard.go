// Package netguard holds the one address blocklist shared by every outbound
// connection that dials a URL an operator typed in: model endpoints and the
// remote backup bucket. Two copies of a security blocklist drift apart, and the
// copy that stops being updated is the one an attacker gets to use.
package netguard

import (
	"net"
	"net/netip"
)

// blockedRanges covers the ranges that are routable enough to reach something
// interesting but are never a legitimate public endpoint. Loopback, private and
// link-local addresses are rejected by Blocked through netip's own predicates.
//
// 198.18.0.0/15 matters in practice: mihomo and other transparent proxies hand
// out fake-IPs from it, so a deployment behind one needs the escape hatch
// rather than a hole in this list.
var blockedRanges = []netip.Prefix{
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("2001:db8::/32"),
}

// Blocked reports whether an address must not be dialled. An address it cannot
// parse counts as blocked: failing closed is the only safe default here.
func Blocked(ip net.IP) bool {
	address, ok := netip.AddrFromSlice(ip)
	if !ok {
		return true
	}
	address = address.Unmap()
	if !address.IsGlobalUnicast() || address.IsPrivate() || address.IsLoopback() || address.IsLinkLocalUnicast() || address.IsUnspecified() {
		return true
	}
	for _, prefix := range blockedRanges {
		if prefix.Contains(address) {
			return true
		}
	}
	return false
}
