package network

import (
	"net/netip"
	"testing"
)

func TestIsAllowed(t *testing.T) {
	allowlist := []string{" Internal.Example. ", "10.1.2.3", "100.64.0.0/10", ""}
	tests := []struct {
		host    string
		addr    string
		allowed bool
	}{
		{"internal.example", "10.0.0.1", true},
		{"INTERNAL.EXAMPLE.", "10.0.0.1", true},
		{"other.example", "10.1.2.3", true},
		{"other.example", "::ffff:10.1.2.3", true},
		{"other.example", "100.100.2.148", true},
		{"other.example", "10.0.0.1", false},
		{"", "192.168.1.1", false},
	}
	for _, tt := range tests {
		if got := IsAllowed(allowlist, tt.host, netip.MustParseAddr(tt.addr)); got != tt.allowed {
			t.Errorf("IsAllowed(%q, %s) = %v, expected %v", tt.host, tt.addr, got, tt.allowed)
		}
	}
	if IsAllowed(nil, "internal.example", netip.MustParseAddr("10.0.0.1")) {
		t.Error("expected nothing to be allowed by an empty allowlist")
	}
}
