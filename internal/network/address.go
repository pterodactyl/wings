// Package network contains helpers for deciding which network destinations
// Wings may connect to on behalf of a user.
package network

import (
	"net/netip"
)

// nat64Prefix is the well-known NAT64 prefix, which embeds an IPv4 address in
// the last 32 bits of the IPv6 address.
var nat64Prefix = netip.MustParsePrefix("64:ff9b::/96")

// specialPurposePrefixes are the special-purpose ranges from the IANA IPv4 and
// IPv6 registries that are not caught by the other checks in IsInternal.
var specialPurposePrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),       // "This network"
	netip.MustParsePrefix("100.64.0.0/10"),   // Shared address space (carrier-grade NAT)
	netip.MustParsePrefix("192.0.0.0/24"),    // IETF protocol assignments
	netip.MustParsePrefix("192.0.2.0/24"),    // Documentation (TEST-NET-1)
	netip.MustParsePrefix("192.88.99.0/24"),  // 6to4 relay anycast
	netip.MustParsePrefix("198.18.0.0/15"),   // Benchmarking
	netip.MustParsePrefix("198.51.100.0/24"), // Documentation (TEST-NET-2)
	netip.MustParsePrefix("203.0.113.0/24"),  // Documentation (TEST-NET-3)
	netip.MustParsePrefix("240.0.0.0/4"),     // Reserved
	netip.MustParsePrefix("::/96"),           // IPv4-compatible (deprecated)
	netip.MustParsePrefix("64:ff9b:1::/48"),  // Local-use IPv4/IPv6 translation
	netip.MustParsePrefix("100::/64"),        // Discard-only
	netip.MustParsePrefix("2001::/23"),       // IETF protocol assignments (including Teredo)
	netip.MustParsePrefix("2001:db8::/32"),   // Documentation
	netip.MustParsePrefix("2002::/16"),       // 6to4
	netip.MustParsePrefix("3fff::/20"),       // Documentation
	netip.MustParsePrefix("5f00::/16"),       // Segment routing (SRv6) SIDs
	netip.MustParsePrefix("fec0::/10"),       // Site-local (deprecated)
}

// IsInternal reports whether the address is anything other than a globally
// reachable unicast address.
func IsInternal(addr netip.Addr) bool {
	// Prefixes never contain an address with a zone, so it has to be removed.
	addr = addr.Unmap().WithZone("")
	if !addr.IsValid() || !addr.IsGlobalUnicast() || addr.IsPrivate() {
		return true
	}
	if nat64Prefix.Contains(addr) {
		b := addr.As16()
		return IsInternal(netip.AddrFrom4([4]byte{b[12], b[13], b[14], b[15]}))
	}
	for _, p := range specialPurposePrefixes {
		if p.Contains(addr) {
			return true
		}
	}
	return false
}
