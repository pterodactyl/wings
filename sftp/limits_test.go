package sftp

import (
	"testing"

	"github.com/pterodactyl/wings/config"
)

func TestConfiguredLimits(t *testing.T) {
	if got := configuredLimits(config.SftpConfiguration{}); got != defaultLimits {
		t.Fatalf("expected the default limits when none are configured, got %+v", got)
	}

	got := configuredLimits(config.SftpConfiguration{MaxConnectionsPerUser: 64, MaxAuthenticatingConnectionsPerIP: 200})
	if got.maxPerUser != 64 || got.maxPendingPerIP != 200 {
		t.Fatalf("expected the configured limits to be used, got %+v", got)
	}
	// All connections may come from a single proxy, so the limit for all addresses
	// cannot be lower than the limit for one.
	if got.maxPending != 200 {
		t.Fatalf("expected the limit for all addresses to allow the limit for one, got %d", got.maxPending)
	}
	if got.handshakeTimeout != defaultLimits.handshakeTimeout {
		t.Fatalf("expected the default handshake timeout, got %s", got.handshakeTimeout)
	}
}
