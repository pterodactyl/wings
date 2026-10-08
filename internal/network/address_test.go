package network

import (
	"net/netip"
	"testing"
)

func TestIsInternal(t *testing.T) {
	internal := []string{
		"0.0.0.0", "0.1.2.3", "10.0.0.1", "100.64.0.1", "100.127.255.254", "127.0.0.1", "127.255.255.254",
		"169.254.169.254", "172.16.0.1", "172.31.255.254", "192.0.0.8", "192.0.2.1", "192.88.99.1",
		"192.168.1.1", "198.18.0.1", "198.19.255.254", "198.51.100.1", "203.0.113.1", "224.0.0.1",
		"239.255.255.250", "240.0.0.1", "255.255.255.255",
		"::", "::1", "::127.0.0.1", "::ffff:127.0.0.1", "::ffff:10.0.0.1", "::ffff:100.64.0.1",
		"64:ff9b::a00:1", "64:ff9b::7f00:1", "64:ff9b:1::1", "100::1", "2001::1", "2001:db8::1",
		"2002:a00:1::1", "3fff::1", "5f00::1", "fc00::1", "fd00::1", "fe80::1", "fec0::1", "ff02::1",
		"fe80::1%eth0", "fec0::1%eth0", "64:ff9b::7f00:1%lo", "2002:a00:1::1%eth0", "100::1%eth0", "fd00::1%eth0",
	}
	external := []string{
		"1.1.1.1", "8.8.8.8", "100.63.255.255", "100.128.0.0", "172.15.255.255", "172.32.0.0",
		"198.17.255.255", "198.20.0.0", "::ffff:1.1.1.1", "64:ff9b::808:808", "2606:4700:4700::1111",
		"2a00:1450:4001::1",
	}

	for _, v := range internal {
		if !IsInternal(netip.MustParseAddr(v)) {
			t.Errorf("expected %s to be internal", v)
		}
	}
	for _, v := range external {
		if IsInternal(netip.MustParseAddr(v)) {
			t.Errorf("expected %s to not be internal", v)
		}
	}
	if !IsInternal(netip.Addr{}) {
		t.Error("expected the zero address to be internal")
	}
}
