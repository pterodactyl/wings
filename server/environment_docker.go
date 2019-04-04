package server

import (
	"fmt"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
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

	// If the container already exists don't hit the user with an error, just return
	// the current information about it which is what we would do when creating the
	// container anyways.
	if _, err := cli.ContainerInspect(ctx, d.Server.Uuid); err == nil {
		return nil
	}

	conf := &container.Config{
		Hostname: "container",
		User: d.Configuration.Container.User,
		AttachStdin: true,
		AttachStdout: true,
		AttachStderr: true,
		OpenStdin: true,
		Tty: true,

		Image: d.Server.Container.Image,
		Env: d.environmentVariables(),

		Labels: map[string]string{
			"Service": "Pterodactyl",
		},
	}

	hostConf := &container.HostConfig{
		Resources: container.Resources{
			Memory: d.Server.Build.MemoryLimit * 1000000,
		},
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