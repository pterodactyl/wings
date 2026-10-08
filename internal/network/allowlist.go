package network

import (
	"net/netip"
	"strings"
)

// IsAllowed reports whether a connection to the address, resolved from the
// host, is allowed by an allowlist configured by the operator. Entries may be
// host names, IP addresses or CIDR ranges.
func IsAllowed(allowlist []string, host string, addr netip.Addr) bool {
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	addr = addr.Unmap()
	for _, entry := range allowlist {
		entry = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(entry)), ".")
		if entry == "" {
			continue
		}
		if entry == host {
			return true
		}
		if allowedAddr, err := netip.ParseAddr(entry); err == nil && allowedAddr.Unmap() == addr {
			return true
		}
		if prefix, err := netip.ParsePrefix(entry); err == nil && prefix.Contains(addr) {
			return true
		}
	}
	return false
}
