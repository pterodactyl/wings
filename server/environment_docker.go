package server

import (
	"fmt"
	"github.com/apex/log"
	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/client"
	"github.com/docker/go-connections/nat"
	"github.com/pterodactyl/wings/config"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

// Defines the base environment for Docker instances running through Wings.
type DockerEnvironment struct {
	sync.RWMutex

	Server *Server

	// The Docker client being used for this instance.
	Client *client.Client

	// Controls the hijacked response stream which exists only when we're attached to
	// the running container instance.
	stream *types.HijackedResponse

	// Holds the stats stream used by the polling commands so that we can easily close
	// it out.
	stats io.ReadCloser
}

// Ensure that the Docker environment is always implementing all of the methods
// from the base environment interface.
var _ Environment = (*DockerEnvironment)(nil)

// Returns the default container mounts for the server instance. This includes the data directory
// for the server as well as any timezone related files if they exist on the host system so that
// servers running within the container will use the correct time.
func (d *DockerEnvironment) getContainerMounts() ([]mount.Mount, error) {
	var m []mount.Mount

	m = append(m, mount.Mount{
		Target:   "/home/container",
		Source:   d.Server.Filesystem.Path(),
		Type:     mount.TypeBind,
		ReadOnly: false,
	})

	// Try to mount in /etc/localtime and /etc/timezone if they exist on the host system.
	if _, err := os.Stat("/etc/localtime"); err != nil {
		if !os.IsNotExist(err) {
			return nil, err
		}
	} else {
		m = append(m, mount.Mount{
			Target:   "/etc/localtime",
			Source:   "/etc/localtime",
			Type:     mount.TypeBind,
			ReadOnly: true,
		})
	}

	if _, err := os.Stat("/etc/timezone"); err != nil {
		if !os.IsNotExist(err) {
			return nil, err
		}
	} else {
		m = append(m, mount.Mount{
			Target:   "/etc/timezone",
			Source:   "/etc/timezone",
			Type:     mount.TypeBind,
			ReadOnly: true,
		})
	}

	return m, nil
}

// Returns the custom mounts for a given server after verifying that they are within a list of
// allowed mount points for the node.
func (d *DockerEnvironment) getCustomMounts() ([]mount.Mount, error) {
	var mounts []mount.Mount

	// TODO: probably need to handle things trying to mount directories that do not exist.
	for _, m := range d.Server.Config().Mounts {
		source := filepath.Clean(m.Source)
		target := filepath.Clean(m.Target)

		logger := d.Server.Log().WithFields(log.Fields{
			"source_path": source,
			"target_path": target,
			"read_only":   m.ReadOnly,
		})

		mounted := false
		for _, allowed := range config.Get().AllowedMounts {
			if !strings.HasPrefix(source, allowed) {
				continue
			}

			mounted = true
			mounts = append(mounts, mount.Mount{
				Source:   source,
				Target:   target,
				Type:     mount.TypeBind,
				ReadOnly: m.ReadOnly,
			})

			break
		}

		if !mounted {
			logger.Warn("skipping custom server mount, not in list of allowed mount points")
		}
	}

	return mounts, nil
}

// Converts the server allocation mappings into a format that can be understood
// by Docker.
func (d *DockerEnvironment) portBindings() nat.PortMap {
	var out = nat.PortMap{}

	for ip, ports := range d.Server.Config().Allocations.Mappings {
		for _, port := range ports {
			// Skip over invalid ports.
			if port < 1 || port > 65535 {
				continue
			}

			binding := []nat.PortBinding{
				{
					HostIP:   ip,
					HostPort: strconv.Itoa(port),
				},
			}

			out[nat.Port(fmt.Sprintf("%d/tcp", port))] = binding
			out[nat.Port(fmt.Sprintf("%d/udp", port))] = binding
		}
	}

	return out
}

// Converts the server allocation mappings into a PortSet that can be understood
// by Docker. This formatting is slightly different than portBindings as it should
// return an empty struct rather than a binding.
//
// To accomplish this, we'll just get the values from portBindings and then set them
// to empty structs. Because why not.
func (d *DockerEnvironment) exposedPorts() nat.PortSet {
	var out = nat.PortSet{}

	for port := range d.portBindings() {
		out[port] = struct{}{}
	}

	return out
}

// Formats the resources available to a server instance in such as way that Docker will
// generate a matching environment in the container.
//
// This will set the actual memory limit on the container using the multiplier which is the
// hard limit for the container (after which will result in a crash). We then set the
// reservation to be the expected memory limit based on simply multiplication.
//
// The swap value is either -1 to disable it, or set to the value of the hard memory limit
// plus the additional swap assigned to the server since Docker expects this value to be
// the same or higher than the memory limit.
func (d *DockerEnvironment) getResourcesForServer() container.Resources {
	return container.Resources{
		Memory:            d.Server.Build().BoundedMemoryLimit(),
		MemoryReservation: d.Server.Build().MemoryLimit * 1_000_000,
		MemorySwap:        d.Server.Build().ConvertedSwap(),
		CPUQuota:          d.Server.Build().ConvertedCpuLimit(),
		CPUPeriod:         100_000,
		CPUShares:         1024,
		BlkioWeight:       d.Server.Build().IoWeight,
		OomKillDisable:    &d.Server.Config().Container.OomDisabled,
		CpusetCpus:        d.Server.Build().Threads,
	}
}
