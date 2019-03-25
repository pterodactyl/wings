package environment

import (
	"github.com/pterodactyl/wings/server"
	"os"
)

// Defines the basic interface that all environments need to implement so that
// a server can be properly controlled.
type Environment interface {
	// Starts a server instance. If the server instance is not in a state where it
	// can be started an error should be returned.
	Start() error

	// Stops a server instance. If the server is already stopped an error should
	// not be returned.
	Stop() error

	// Determines if the server instance exists. For example, in a docker environment
	// this should confirm that the container is created and in a bootable state. In
	// a basic CLI environment this can probably just return true right away.
	Exists() bool

	// Terminates a running server instance using the provided signal. If the server
	// is not running no error should be returned.
	Terminate(signal os.Signal) error
}

// Defines an environment controller for a server instance. This can either be
// a docker environment where the server is running in a container, or a host
// CLI environment where it is not running in a container at all (theoretically).
type Controller struct {
	// The server instance attached to this environment.
	Server *server.Server
}
