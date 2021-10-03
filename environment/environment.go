package environment

import (
	"context"
	"os"

	"github.com/pterodactyl/wings/events"
)

const (
	ConsoleOutputEvent       = "console output"
	StateChangeEvent         = "state change"
	ResourceEvent            = "resources"
	DockerImagePullStarted   = "docker image pull started"
	DockerImagePullStatus    = "docker image pull status"
	DockerImagePullCompleted = "docker image pull completed"
)

const (
	ProcessOfflineState  = "offline"
	ProcessStartingState = "starting"
	ProcessRunningState  = "running"
	ProcessStoppingState = "stopping"
)

// Defines the basic interface that all environments need to implement so that
// a server can be properly controlled.
type ProcessEnvironment interface {
	// Returns the name of the environment.
	Type() string

	// Returns the environment configuration to the caller.
	Config() *Configuration

	// Returns an event emitter instance that can be hooked into to listen for different
	// events that are fired by the environment. This should not allow someone to publish
	// events, only subscribe to them.
	Events() *events.EventBus

	// Determines if the server instance exists. For example, in a docker environment
	// this should confirm that the container is created and in a bootable state. In
	// a basic CLI environment this can probably just return true right away.
	Exists() (bool, error)

	// IsRunning determines if the environment is currently active and running
	// a server process for this specific server instance.
	IsRunning(ctx context.Context) (bool, error)

	// Performs an update of server resource limits without actually stopping the server
	// process. This only executes if the environment supports it, otherwise it is
	// a no-op.
	InSituUpdate() error

	// Runs before the environment is started. If an error is returned starting will
	// not occur, otherwise proceeds as normal.
	OnBeforeStart(ctx context.Context) error

	// Starts a server instance. If the server instance is not in a state where it
	// can be started an error should be returned.
	Start(ctx context.Context) error

	// Stops a server instance. If the server is already stopped an error should
	// not be returned.
	Stop() error

	// Waits for a server instance to stop gracefully. If the server is still detected
	// as running after seconds, an error will be returned, or the server will be terminated
	// depending on the value of the second argument.
	WaitForStop(seconds uint, terminate bool) error

	// Terminates a running server instance using the provided signal. If the server
	// is not running no error should be returned.
	Terminate(signal os.Signal) error

	// Destroys the environment removing any containers that were created (in Docker
	// environments at least).
	Destroy() error

	// Returns the exit state of the process. The first result is the exit code, the second
	// determines if the process was killed by the system OOM killer.
	ExitState() (uint32, bool, error)

	// Creates the necessary environment for running the server process. For example,
	// in the Docker environment create will create a new container instance for the
	// server.
	Create() error

	// Attach attaches to the server console environment and allows piping the output
	// to a websocket or other internal tool to monitor output. Also allows you to later
	// send data into the environment's stdin.
	Attach(ctx context.Context) error

	// Sends the provided command to the running server instance.
	SendCommand(string) error

	// Reads the log file for the process from the end backwards until the provided
	// number of lines is met.
	Readlog(int) ([]string, error)

	// Returns the current state of the environment.
	State() string

	// Sets the current state of the environment. In general you should let the environment
	// handle this itself, but there are some scenarios where it is helpful for the server
	// to update the state externally (e.g. starting -> started).
	SetState(string)

	// Uptime returns the current environment uptime in milliseconds. This is
	// the time that has passed since it was last started.
	Uptime(ctx context.Context) (int64, error)
}
