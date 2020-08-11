package environment

import (
	"fmt"
	"github.com/pkg/errors"
	"github.com/pterodactyl/wings/environment"
	"github.com/pterodactyl/wings/system"
)

// Returns the current environment state.
func (d *DockerEnvironment) State() string {
	d.stMu.RLock()
	defer d.stMu.RUnlock()

	return d.st
}

// Sets the state of the environment. This emits an event that server's can hook into to
// take their own actions and track their own state based on the environment.
func (d *DockerEnvironment) setState(state string) error {
	if state != system.ProcessOfflineState &&
		state != system.ProcessStartingState &&
		state != system.ProcessRunningState &&
		state != system.ProcessStoppingState {
		return errors.New(fmt.Sprintf("invalid server state received: %s", state))
	}

	// Get the current state of the environment before changing it.
	prevState := d.State()

	// Emit the event to any listeners that are currently registered.
	if prevState != state {
		// If the state changed make sure we update the internal tracking to note that.
		d.stMu.Lock()
		d.st = state
		d.stMu.Unlock()

		d.Events().Publish(environment.StateChangeEvent, d.State())
	}

	return nil
}
