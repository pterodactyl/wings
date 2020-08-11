package environment

import (
	"context"
	"github.com/docker/docker/api/types"
	"github.com/docker/docker/client"
	"github.com/pkg/errors"
	"github.com/pterodactyl/wings/api"
	"github.com/pterodactyl/wings/environment"
	"github.com/pterodactyl/wings/events"
	"io"
	"sync"
)

// Ensure that the Docker environment is always implementing all of the methods
// from the base environment interface.
var _ environment.ProcessEnvironment = (*DockerEnvironment)(nil)

type DockerEnvironment struct {
	mu      sync.RWMutex
	eventMu sync.Mutex

	// The public identifier for this environment. In this case it is the Docker container
	// name that will be used for all instances created under it.
	Id string

	// The environment configuration.
	Configuration *environment.Configuration

	// The Docker image to use for this environment.
	image string

	// The stop configuration for the environment
	stop api.ProcessStopConfiguration

	// The Docker client being used for this instance.
	client *client.Client

	// Controls the hijacked response stream which exists only when we're attached to
	// the running container instance.
	stream *types.HijackedResponse

	// Holds the stats stream used by the polling commands so that we can easily close it out.
	stats io.ReadCloser

	emitter *events.EventBus

	// Tracks the environment state.
	st   string
	stMu sync.RWMutex
}

// Creates a new base Docker environment. The ID passed through will be the ID that is used to
// reference the container from here on out. This should be unique per-server (we use the UUID
// by default). The container does not need to exist at this point.
func NewDocker(id string, image string, s api.ProcessStopConfiguration, c environment.Configuration) (*DockerEnvironment, error) {
	cli, err := client.NewClientWithOpts(client.FromEnv)
	if err != nil {
		return nil, err
	}

	e := &DockerEnvironment{
		Id:            id,
		image:         image,
		stop:          s,
		Configuration: &c,
		client:        cli,
	}

	return e, nil
}

func (d *DockerEnvironment) Type() string {
	return "docker"
}

// Set if this process is currently attached to the process.
func (d *DockerEnvironment) SetStream(s *types.HijackedResponse) {
	d.mu.Lock()
	d.stream = s
	d.mu.Unlock()
}

// Determine if the this process is currently attached to the container.
func (d *DockerEnvironment) IsAttached() bool {
	d.mu.RLock()
	defer d.mu.RUnlock()

	return d.stream != nil
}

func (d *DockerEnvironment) Events() *events.EventBus {
	d.eventMu.Lock()
	defer d.eventMu.Unlock()

	if d.emitter == nil {
		d.emitter = events.New()
	}

	return d.emitter
}

// Determines if the container exists in this environment. The ID passed through should be the
// server UUID since containers are created utilizing the server UUID as the name and docker
// will work fine when using the container name as the lookup parameter in addition to the longer
// ID auto-assigned when the container is created.
func (d *DockerEnvironment) Exists() (bool, error) {
	_, err := d.client.ContainerInspect(context.Background(), d.Id)

	if err != nil {
		// If this error is because the container instance wasn't found via Docker we
		// can safely ignore the error and just return false.
		if client.IsErrNotFound(err) {
			return false, nil
		}

		return false, err
	}

	return true, nil
}

// Determines if the server's docker container is currently running. If there is no container
// present, an error will be raised (since this shouldn't be a case that ever happens under
// correctly developed circumstances).
//
// You can confirm if the instance wasn't found by using client.IsErrNotFound from the Docker
// API.
//
// @see docker/client/errors.go
func (d *DockerEnvironment) IsRunning() (bool, error) {
	c, err := d.client.ContainerInspect(context.Background(), d.Id)
	if err != nil {
		return false, err
	}

	return c.State.Running, nil
}

// Determine the container exit state and return the exit code and wether or not
// the container was killed by the OOM killer.
func (d *DockerEnvironment) ExitState() (uint32, bool, error) {
	c, err := d.client.ContainerInspect(context.Background(), d.Id)
	if err != nil {
		// I'm not entirely sure how this can happen to be honest. I tried deleting a
		// container _while_ a server was running and wings gracefully saw the crash and
		// created a new container for it.
		//
		// However, someone reported an error in Discord about this scenario happening,
		// so I guess this should prevent it? They didn't tell me how they caused it though
		// so that's a mystery that will have to go unsolved.
		//
		// @see https://github.com/pterodactyl/panel/issues/2003
		if client.IsErrNotFound(err) {
			return 1, false, nil
		}

		return 0, false, errors.WithStack(err)
	}

	return uint32(c.State.ExitCode), c.State.OOMKilled, nil
}
