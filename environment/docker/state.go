package docker

import (
	"emperror.dev/errors"
	"fmt"
	"github.com/pterodactyl/wings/environment"
)

func (e *Environment) State() string {
	return e.st.Load()
}

// Sets the state of the environment. This emits an event that server's can hook into to
// take their own actions and track their own state based on the environment.
func (e *Environment) SetState(state string) {
	if state != environment.ProcessOfflineState &&
		state != environment.ProcessStartingState &&
		state != environment.ProcessRunningState &&
		state != environment.ProcessStoppingState {
		panic(errors.New(fmt.Sprintf("invalid server state received: %s", state)))
	}

	// Emit the event to any listeners that are currently registered.
	if e.State() != state {
		// If the state changed make sure we update the internal tracking to note that.
		e.st.Store(state)
		e.Events().Publish(environment.StateChangeEvent, state)
	}
}
