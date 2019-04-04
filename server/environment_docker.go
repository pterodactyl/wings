package server

import (
	"fmt"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/client"
	"github.com/docker/docker/daemon/logger/jsonfilelog"
	"github.com/docker/go-connections/nat"
	"golang.org/x/net/context"
	"os"
	"strings"
)

// Defines the docker configuration used by the daemon when interacting with
// containers and networks on the system.
type DockerConfiguration struct {
	Container struct {
		User string
	}

	// Network configuration that should be used when creating a new network
	// for containers run through the daemon.
	Network struct {
		// The interface that should be used to create the network. Must not conflict
		// with any other interfaces in use by Docker or on the system.
		Interface string

		// The name of the network to use. If this network already exists it will not
		// be created. If it is not found, a new network will be created using the interface
		// defined.
		Name string
	}

	// If true, container images will be updated when a server starts if there
	// is an update available. If false the daemon will not attempt updates and will
	// defer to the host system to manage image updates.
	UpdateImages bool `yaml:"update_images"`

	// The location of the Docker socket.
	Socket string

	// Defines the location of the timezone file on the host system that should
	// be mounted into the created containers so that they all use the same time.
	TimezonePath string `yaml:"timezone_path"`
}

type DockerEnvironment struct {
	Server *Server

	// Defines the configuration for the Docker instance that will allow us to connect
	// and create and modify containers.
	Configuration DockerConfiguration
}

// Ensure that the Docker environment is always implementing all of the methods
// from the base environment interface.
var _ Environment = (*DockerEnvironment)(nil)

func (d *DockerEnvironment) Exists() bool {
	return true
}

func (d *DockerEnvironment) Start() error {
	return nil
}

func (d *DockerEnvironment) Stop() error {
	return nil
}

func (d *DockerEnvironment) Terminate(signal os.Signal) error {
	return nil
}

// Creates a new container for the server using all of the data that is currently
// available for it. If the container already exists it will be returned.
func (d *DockerEnvironment) Create() error {
	ctx := context.Background()
	cli, err := client.NewEnvClient()
	if err != nil {
		return err
	}

	var oomDisabled = true

	// If the container already exists don't hit the user with an error, just return
	// the current information about it which is what we would do when creating the
	// container anyways.
	if _, err := cli.ContainerInspect(ctx, d.Server.Uuid); err == nil {
		return nil
	}

	conf := &container.Config{
		Hostname:     "container",
		User:         d.Configuration.Container.User,
		AttachStdin:  true,
		AttachStdout: true,
		AttachStderr: true,
		OpenStdin:    true,
		Tty:          true,

		ExposedPorts: d.exposedPorts(),

		Image: d.Server.Container.Image,
		Env:   d.environmentVariables(),

		Labels: map[string]string{
			"Service": "Pterodactyl",
		},
	}

	hostConf := &container.HostConfig{
		PortBindings: d.portBindings(),

		// Configure the mounts for this container. First mount the server data directory
		// into the container as a r/w bind. Additionally mount the host timezone data into
		// the container as a readonly bind so that software running in the container uses
		// the same time as the host system.
		Mounts: []mount.Mount{
			{
				Target:   "/home/container",
				Source:   d.Server.Filesystem().Path(),
				Type:     mount.TypeBind,
				ReadOnly: false,
			},
			{
				Target:   d.Configuration.TimezonePath,
				Source:   d.Configuration.TimezonePath,
				Type:     mount.TypeBind,
				ReadOnly: true,
			},
		},

		// Configure the /tmp folder mapping in containers. This is necessary for some
		// games that need to make use of it for downloads and other installation processes.
		Tmpfs: map[string]string{
			"/tmp": "rw,exec,nosuid,size=50M",
		},

		// Define resource limits for the container based on the data passed through
		// from the Panel.
		Resources: container.Resources{
			// @todo memory limit should be slightly higher than the reservation
			Memory:            d.Server.Build.MemoryLimit * 1000000,
			MemoryReservation: d.Server.Build.MemoryLimit * 1000000,
			MemorySwap:        d.Server.Build.ConvertedSwap(),

			CPUQuota:  d.Server.Build.ConvertedCpuLimit(),
			CPUPeriod: 100000,
			CPUShares: 1024,

			BlkioWeight:    d.Server.Build.IoWeight,
			OomKillDisable: &oomDisabled,
		},

		// @todo make this configurable again
		DNS: []string{"1.1.1.1", "8.8.8.8"},

		// Configure logging for the container to make it easier on the Daemon to grab
		// the server output. Ensure that we don't use too much space on the host machine
		// since we only need it for the last few hundred lines of output and don't care
		// about anything else in it.
		LogConfig: container.LogConfig{
			Type: jsonfilelog.Name,
			Config: map[string]string{
				"max-size": "5m",
				"max-file": "1",
			},
		},

		SecurityOpt:    []string{"no-new-privileges"},
		ReadonlyRootfs: true,
		CapDrop: []string{
			"setpcap", "mknod", "audit_write", "net_raw", "dac_override",
			"fowner", "fsetid", "net_bind_service", "sys_chroot", "setfcap",
		},
		NetworkMode: "pterodactyl_nw",
	}

	if _, err := cli.ContainerCreate(ctx, conf, hostConf, nil, d.Server.Uuid); err != nil {
		return err
	}

	return nil
}

// Returns the environment variables for a server in KEY="VALUE" form.
func (d *DockerEnvironment) environmentVariables() []string {
	var out = []string{
		fmt.Sprintf("STARTUP=%s", d.Server.Invocation),
	}

	for k, v := range d.Server.EnvVars {
		if strings.ToUpper(k) == "STARTUP" {
			continue
		}

		out = append(out, fmt.Sprintf("%s=\"%s\"", strings.ToUpper(k), v))
	}

	return out
}

func (d *DockerEnvironment) volumes() map[string]struct{} {
	return nil
}

// Converts the server allocation mappings into a format that can be understood
// by Docker.
func (d *DockerEnvironment) portBindings() nat.PortMap {
	var out = nat.PortMap{}

	for ip, ports := range d.Server.Allocations.Mappings {
		for _, port := range ports {
			// Skip over invalid ports.
			if port < 0 || port > 65535 {
				continue
			}

			binding := []nat.PortBinding{
				{
					HostIP:   ip,
					HostPort: string(port),
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
