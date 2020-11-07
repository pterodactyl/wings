package docker

import (
	"fmt"
	"github.com/pkg/errors"
	"github.com/pterodactyl/wings/environment"
)

// Sets the state of the environment. This emits an event that server's can hook into to
// take their own actions and track their own state based on the environment.
func (e *Environment) setState(state string) error {
	if state != environment.ProcessOfflineState &&
		state != environment.ProcessStartingState &&
		state != environment.ProcessRunningState &&
		state != environment.ProcessStoppingState {
		return errors.New(fmt.Sprintf("invalid server state received: %s", state))
	}

	// Get the current state of the environment before changing it.
	prevState := e.State.Load()

	// Emit the event to any listeners that are currently registered.
	if prevState != state {
		// If the state changed make sure we update the internal tracking to note that.
		e.State.Store(state)
		e.Events().Publish(environment.StateChangeEvent, state)
	}

	return nil
}
