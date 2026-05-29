package environment

import (
	"fmt"
	"strconv"

	"github.com/docker/go-connections/nat"

	"github.com/pterodactyl/wings/config"
)

// Defines the allocations available for a given server. When using the Docker environment
// driver these correspond to mappings for the container that allow external connections.
type Allocations struct {
	// ForceOutgoingIP causes a dedicated bridge network to be created for the
	// server with a special option, causing Docker to SNAT outgoing traffic to
	// the DefaultMapping's IP. This is important to servers which rely on external
	// services that check the IP of the server (Source Engine servers, for example).
	ForceOutgoingIP bool `json:"force_outgoing_ip"`
	// Defines the default allocation that should be used for this server. This is
	// what will be used for {SERVER_IP} and {SERVER_PORT} when modifying configuration
	// files or the startup arguments for a server.
	DefaultMapping struct {
		Ip   string `json:"ip"`
		Port int    `json:"port"`
	} `json:"default"`

	// Mappings contains all the ports that should be assigned to a given server
	// attached to the IP they correspond to.
	Mappings map[string][]int `json:"mappings"`
}

// Converts the server allocation mappings into a format that can be understood by Docker. While
// we do strive to support multiple environments, using Docker's standardized format for the
// bindings certainly makes life a little easier for managing things.
//
// You'll want to use DockerBindings() if you need to re-map 127.0.0.1 to the Docker interface.
func (a *Allocations) Bindings() nat.PortMap {
	out := nat.PortMap{}

	for ip, ports := range a.Mappings {
		for _, port := range ports {
			// Skip over invalid ports.
			if port < 1 || port > 65535 {
				continue
			}

			binding := nat.PortBinding{
				HostIP:   ip,
				HostPort: strconv.Itoa(port),
			}

			tcp := nat.Port(fmt.Sprintf("%d/tcp", port))
			udp := nat.Port(fmt.Sprintf("%d/udp", port))

			out[tcp] = append(out[tcp], binding)
			out[udp] = append(out[udp], binding)
		}
	}

	return out
}

// Returns the bindings for the server in a way that is supported correctly by Docker. This replaces
// any reference to 127.0.0.1 with the IP of the pterodactyl0 network interface which will allow the
// server to operate on a local address while still being accessible by other containers.
func (a *Allocations) DockerBindings() nat.PortMap {
	iface := config.Get().Docker.Network.Interface

	out := a.Bindings()
	// Loop over all the bindings for this container, and convert any that reference 127.0.0.1
	// to use the pterodactyl0 network interface IP, as that is the true local for what people are
	// trying to do when creating servers.
	for p, binds := range out {
		for i, alloc := range binds {
			if alloc.HostIP != "127.0.0.1" {
				continue
			}

			// If using ISPN just delete the local allocation from the server.
			if config.Get().Docker.Network.ISPN {
				out[p] = append(out[p][:i], out[p][i+1:]...)
			} else {
				out[p][i] = nat.PortBinding{
					HostIP:   iface,
					HostPort: alloc.HostPort,
				}
			}
		}
	}

	return out
}

// Converts the server allocation mappings into a PortSet that can be understood
// by Docker. This formatting is slightly different than "Bindings" as it should
// return an empty struct rather than a binding.
//
// To accomplish this, we'll just get the values from "DockerBindings" and then set them
// to empty structs. Because why not.
func (a *Allocations) Exposed() nat.PortSet {
	out := nat.PortSet{}

	for port := range a.DockerBindings() {
		out[port] = struct{}{}
	}

	return out
}
