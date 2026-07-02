package environment

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"

	"emperror.dev/errors"
	"github.com/apex/log"
)

const (
	egressChainName    = "PTERO-EGRESS"
	whitelistChainName = "PTERO-EGRESS-WHITELIST"
)

// ConfigureEgressFirewall installs iptables/ip6tables rules on the given bridge
// interface that restrict outbound traffic from containers to only the provided
// destination ports. All other outbound traffic is dropped.
//
// The rules are applied to the filter table's FORWARD chain so they affect any
// container attached to the bridge, regardless of image or egg.
func ConfigureEgressFirewall(bridgeName string, allowedPorts []int) error {
	if bridgeName == "" {
		return errors.New("environment/firewall: bridge name cannot be empty")
	}

	if len(allowedPorts) == 0 {
		log.Warn("environment/firewall: allowed outbound ports list is empty; all outbound container traffic will be dropped")
	}

	iptables, err := exec.LookPath("iptables")
	if err != nil {
		return errors.Wrap(err, "environment/firewall: iptables not found in PATH")
	}

	ip6tables, err := exec.LookPath("ip6tables")
	if err != nil {
		return errors.Wrap(err, "environment/firewall: ip6tables not found in PATH")
	}

	if err := configureFamily(iptables, bridgeName, allowedPorts, false); err != nil {
		return errors.Wrap(err, "environment/firewall: failed to configure IPv4 egress rules")
	}

	if err := configureFamily(ip6tables, bridgeName, allowedPorts, true); err != nil {
		return errors.Wrap(err, "environment/firewall: failed to configure IPv6 egress rules")
	}

	log.WithFields(log.Fields{
		"bridge": bridgeName,
		"ports":  allowedPorts,
	}).Info("configured container egress firewall")

	return nil
}

// ClearEgressFirewall removes any rules previously installed by
// ConfigureEgressFirewall for the given bridge. It is safe to call even if no
// rules exist.
func ClearEgressFirewall(bridgeName string) error {
	if bridgeName == "" {
		return nil
	}

	iptables, err := exec.LookPath("iptables")
	if err != nil {
		log.WithError(err).Debug("environment/firewall: iptables not found, skipping egress firewall cleanup")
		return nil
	}

	ip6tables, err := exec.LookPath("ip6tables")
	if err != nil {
		log.WithError(err).Debug("environment/firewall: ip6tables not found, skipping IPv6 egress firewall cleanup")
	} else {
		if err := clearFamily(ip6tables, bridgeName); err != nil {
			log.WithError(err).Warn("environment/firewall: failed to clear IPv6 egress rules")
		}
	}

	if err := clearFamily(iptables, bridgeName); err != nil {
		return errors.Wrap(err, "environment/firewall: failed to clear IPv4 egress rules")
	}

	log.WithField("bridge", bridgeName).Info("cleared container egress firewall")

	return nil
}

func configureFamily(binary, bridgeName string, allowedPorts []int, ipv6 bool) error {
	// Ensure the chain exists. Ignore errors such as the chain already existing.
	_ = runIptables(binary, "-N", egressChainName)
	_ = runIptables(binary, "-N", whitelistChainName)

	// Flush any existing rules in the chain so we can rebuild it cleanly.
	if err := runIptables(binary, "-F", egressChainName); err != nil {
		return err
	}
	if err := runIptables(binary, "-F", whitelistChainName); err != nil {
		return err
	}

	// Allow return traffic for established connections.
	if err := runIptables(binary, "-A", egressChainName, "-m", "state", "--state", "ESTABLISHED,RELATED", "-j", "RETURN"); err != nil {
		return err
	}

	// Allow ICMP so basic connectivity and path-MTU discovery work.
	if ipv6 {
		// IPv6 neighbor discovery and other ICMPv6 messages are required for the
		// bridge to function at all.
		if err := runIptables(binary, "-A", egressChainName, "-p", "icmpv6", "-j", "RETURN"); err != nil {
			return err
		}
	} else {
		// Allow IPv4 echo-request for basic connectivity checks. This does not
		// meaningfully help an attacker and makes debugging much easier.
		if err := runIptables(binary, "-A", egressChainName, "-p", "icmp", "--icmp-type", "echo-request", "-j", "RETURN"); err != nil {
			return err
		}
	}

	// Attach the whitelist chain so specific container IPs can be exempted.
	_ = runIptables(binary, "-D", egressChainName, "-j", whitelistChainName)
	if err := runIptables(binary, "-I", egressChainName, "3", "-j", whitelistChainName); err != nil {
		return err
	}

	// Allow the configured destination ports for both TCP and UDP.
	for _, port := range allowedPorts {
		p := strconv.Itoa(port)
		if err := runIptables(binary, "-A", egressChainName, "-p", "udp", "--dport", p, "-j", "RETURN"); err != nil {
			return err
		}
		if err := runIptables(binary, "-A", egressChainName, "-p", "tcp", "--dport", p, "-j", "RETURN"); err != nil {
			return err
		}
	}

	// Drop everything else.
	if err := runIptables(binary, "-A", egressChainName, "-j", "DROP"); err != nil {
		return err
	}

	// Remove any stale jump rule from FORWARD, then insert our chain at the top.
	_ = runIptables(binary, "-D", "FORWARD", "-i", bridgeName, "!", "-o", bridgeName, "-j", egressChainName)

	if err := runIptables(binary, "-I", "FORWARD", "1", "-i", bridgeName, "!", "-o", bridgeName, "-j", egressChainName); err != nil {
		return err
	}

	return nil
}

func clearFamily(binary, bridgeName string) error {
	_ = runIptables(binary, "-D", "FORWARD", "-i", bridgeName, "!", "-o", bridgeName, "-j", egressChainName)
	_ = runIptables(binary, "-F", egressChainName)
	_ = runIptables(binary, "-X", egressChainName)
	return nil
}

// AddFirewallWhitelistIP adds an IP address to the egress whitelist so traffic
// from that source is not restricted. The operation is idempotent.
func AddFirewallWhitelistIP(ip string) error {
	if ip == "" {
		return nil
	}

	iptables, err := exec.LookPath("iptables")
	if err != nil {
		return errors.Wrap(err, "environment/firewall: iptables not found in PATH")
	}

	ip6tables, err := exec.LookPath("ip6tables")
	if err != nil {
		return errors.Wrap(err, "environment/firewall: ip6tables not found in PATH")
	}

	if err := addWhitelistFamily(iptables, ip); err != nil {
		return errors.Wrap(err, "environment/firewall: failed to add IPv4 whitelist")
	}

	if err := addWhitelistFamily(ip6tables, ip); err != nil {
		return errors.Wrap(err, "environment/firewall: failed to add IPv6 whitelist")
	}

	return nil
}

// RemoveFirewallWhitelistIP removes an IP address from the egress whitelist.
func RemoveFirewallWhitelistIP(ip string) error {
	if ip == "" {
		return nil
	}

	iptables, err := exec.LookPath("iptables")
	if err != nil {
		return errors.Wrap(err, "environment/firewall: iptables not found in PATH")
	}

	ip6tables, err := exec.LookPath("ip6tables")
	if err != nil {
		return errors.Wrap(err, "environment/firewall: ip6tables not found in PATH")
	}

	_ = runIptables(iptables, "-D", whitelistChainName, "-s", ip, "-j", "RETURN")
	_ = runIptables(ip6tables, "-D", whitelistChainName, "-s", ip, "-j", "RETURN")
	return nil
}

// ClearFirewallWhitelist removes all IP addresses from the egress whitelist.
func ClearFirewallWhitelist() error {
	iptables, err := exec.LookPath("iptables")
	if err != nil {
		return errors.Wrap(err, "environment/firewall: iptables not found in PATH")
	}

	ip6tables, err := exec.LookPath("ip6tables")
	if err != nil {
		return errors.Wrap(err, "environment/firewall: ip6tables not found in PATH")
	}

	_ = runIptables(iptables, "-F", whitelistChainName)
	_ = runIptables(ip6tables, "-F", whitelistChainName)
	return nil
}

func addWhitelistFamily(binary, ip string) error {
	// Remove any existing rule for this IP to avoid duplicates.
	_ = runIptables(binary, "-D", whitelistChainName, "-s", ip, "-j", "RETURN")
	return runIptables(binary, "-A", whitelistChainName, "-s", ip, "-j", "RETURN")
}

func runIptables(binary string, args ...string) error {
	cmd := exec.Command(binary, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return errors.New(
			fmt.Sprintf("%s %s failed: %s", binary, strings.Join(args, " "), strings.TrimSpace(string(out))),
		)
	}
	return nil
}
