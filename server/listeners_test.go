package server

import (
	"sync"
	"testing"

	"github.com/pterodactyl/wings/environment"
	"github.com/pterodactyl/wings/remote"
)

// stateEnvironment is an environment that records the states it is set to.
type stateEnvironment struct {
	environment.ProcessEnvironment
	mu     sync.Mutex
	state  string
	states []string
}

func (e *stateEnvironment) State() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.state
}

func (e *stateEnvironment) SetState(st string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.state = st
	e.states = append(e.states, st)
}

func TestConsoleOutputDoesNotChangeState(t *testing.T) {
	for _, st := range []string{environment.ProcessStartingState, environment.ProcessRunningState} {
		t.Run(st, func(t *testing.T) {
			s, err := New(nil)
			if err != nil {
				t.Fatal(err)
			}
			env := &stateEnvironment{state: st}
			s.Environment = env
			s.procConfig = &remote.ProcessConfiguration{}
			s.procConfig.Stop.Type = remote.ProcessStopCommand
			s.procConfig.Stop.Value = "stop"

			// Output that matches the stop command.
			for i := 0; i < 3; i++ {
				s.onConsoleOutput([]byte("stop"))
			}

			if env.State() != st {
				t.Fatalf("expected server to remain %s, got %s (transitions: %v)", st, env.State(), env.states)
			}
		})
	}
}
