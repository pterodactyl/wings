package server

import (
	"context"
	"encoding/json"
	"os"

	"github.com/apex/log"

	"github.com/pterodactyl/wings/environment"
	"github.com/pterodactyl/wings/environment/docker"
)

// DefaultFirewallWhitelistPath is the JSON file containing server UUIDs that
// should be exempt from the egress firewall.
const DefaultFirewallWhitelistPath = "/etc/pterodactyl/firewall-whitelist.json"

// IsFirewallWhitelisted reads the whitelist file and returns true if the given
// server UUID is listed. The file is read on every call so changes take effect
// immediately.
func IsFirewallWhitelisted(uuid string) bool {
	data, err := os.ReadFile(DefaultFirewallWhitelistPath)
	if err != nil {
		if !os.IsNotExist(err) {
			log.WithError(err).Warn("firewall whitelist: failed to read whitelist file")
		}
		return false
	}

	var uuids []string
	if err := json.Unmarshal(data, &uuids); err != nil {
		log.WithError(err).Warn("firewall whitelist: failed to parse whitelist file")
		return false
	}

	for _, u := range uuids {
		if u == uuid {
			return true
		}
	}
	return false
}

// SyncFirewallWhitelist applies whitelist rules for all servers known to the
// manager. It should be called once at startup and whenever the firewall is
// reconfigured.
func SyncFirewallWhitelist(m *Manager) {
	for _, s := range m.All() {
		UpdateServerFirewallWhitelist(s)
	}
}

// UpdateServerFirewallWhitelist adds or removes the server's container IP from
// the egress firewall whitelist based on the current whitelist file.
func UpdateServerFirewallWhitelist(s *Server) {
	ip := getServerContainerIP(s)
	if ip == "" {
		return
	}

	if IsFirewallWhitelisted(s.ID()) {
		if err := environment.AddFirewallWhitelistIP(ip); err != nil {
			log.WithError(err).WithField("server", s.ID()).WithField("ip", ip).Warn("firewall whitelist: failed to add IP")
		} else {
			log.WithField("server", s.ID()).WithField("ip", ip).Debug("firewall whitelist: added IP")
		}
	} else {
		if err := environment.RemoveFirewallWhitelistIP(ip); err != nil {
			log.WithError(err).WithField("server", s.ID()).WithField("ip", ip).Warn("firewall whitelist: failed to remove IP")
		}
	}
}

// RemoveServerFirewallWhitelist removes the server's container IP from the
// egress firewall whitelist regardless of the whitelist file.
func RemoveServerFirewallWhitelist(s *Server) {
	ip := getServerContainerIP(s)
	if ip == "" {
		return
	}

	if err := environment.RemoveFirewallWhitelistIP(ip); err != nil {
		log.WithError(err).WithField("server", s.ID()).WithField("ip", ip).Warn("firewall whitelist: failed to remove IP")
	}
}

// getServerContainerIP returns the IPv4 address of the server's Docker
// container on the Wings network.
func getServerContainerIP(s *Server) string {
	de, ok := s.Environment.(*docker.Environment)
	if !ok {
		return ""
	}

	c, err := de.ContainerInspect(context.Background())
	if err != nil {
		log.WithError(err).WithField("server", s.ID()).Debug("firewall whitelist: failed to inspect container")
		return ""
	}

	return c.NetworkSettings.IPAddress
}
