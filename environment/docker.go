package environment

import (
	"os"
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

type Docker struct {
	// Defines the configuration for the Docker instance that will allow us to connect
	// and create and modify containers.
	Configuration DockerConfiguration
}

// Ensure that the Docker environment is always implementing all of the methods
// from the base environment interface.
var _ Environment = (*Docker)(nil)

func (d *Docker) Exists() bool {
	return true
}

func (d *Docker) Start() error {
	return nil
}

func (d *Docker) Stop() error {
	return nil
}

func (d *Docker) Terminate(signal os.Signal) error {
	return nil
}
