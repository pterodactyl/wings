package environment

import (
    "github.com/pterodactyl/wings"
    "os"
)

type Docker struct {
    *Controller

    // Defines the configuration for the Docker instance that will allow us to connect
    // and create and modify containers.
    Configuration wings.DockerConfiguration
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
