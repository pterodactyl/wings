package server

import (
	"encoding/json"
	"fmt"
	"github.com/pkg/errors"
	"github.com/pterodactyl/wings/config"
	"io"
	"io/ioutil"
	"os"
	"sync"
)

var stateMutex sync.Mutex

const (
	ProcessOfflineState  = "offline"
	ProcessStartingState = "starting"
	ProcessRunningState  = "running"
	ProcessStoppingState = "stopping"
)

// Returns the state of the servers.
func getServerStates() (map[string]string, error) {
	// Request a lock after we check if the file exists.
	stateMutex.Lock()
	defer stateMutex.Unlock()

	// Open the states file.
	f, err := os.OpenFile(config.Get().System.GetStatesPath(), os.O_RDONLY|os.O_CREATE, 0644)
	if err != nil {
		return nil, errors.WithStack(err)
	}
	defer f.Close()

	// Convert the json object to a map.
	states := map[string]string{}
	if err := json.NewDecoder(f).Decode(&states); err != nil && err != io.EOF {
		return nil, errors.WithStack(err)
	}

	return states, nil
}

// saveServerStates .
func saveServerStates() error {
	// Get the states of all servers on the daemon.
	states := map[string]string{}
	for _, s := range GetServers().All() {
		states[s.Uuid] = s.GetState()
	}

	// Convert the map to a json object.
	data, err := json.Marshal(states)
	if err != nil {
		return errors.WithStack(err)
	}

	stateMutex.Lock()
	defer stateMutex.Unlock()

	// Write the data to the file
	if err := ioutil.WriteFile(config.Get().System.GetStatesPath(), data, 0644); err != nil {
		return errors.WithStack(err)
	}

	return nil
}

// Sets the state of the server internally. This function handles crash detection as
// well as reporting to event listeners for the server.
func (s *Server) SetState(state string) error {
	if state != ProcessOfflineState && state != ProcessStartingState && state != ProcessRunningState && state != ProcessStoppingState {
		return errors.New(fmt.Sprintf("invalid server state received: %s", state))
	}

	prevState := s.GetState()

	// Update the currently tracked state for the server.
	s.Proc().setInternalState(state)

	// Emit the event to any listeners that are currently registered.
	if prevState != state {
		s.Log().WithField("status", s.Proc().State).Debug("saw server status change event")
		s.Events().Publish(StatusEvent, s.Proc().State)
	}

	// Persist this change to the disk immediately so that should the Daemon be stopped or
	// crash we can immediately restore the server state.
	//
	// This really only makes a difference if all of the Docker containers are also stopped,
	// but this was a highly requested feature and isn't hard to work with, so lets do it.
	//
	// We also get the benefit of server status changes always propagating corrected configurations
	// to the disk should we forget to do it elsewhere.
	go func() {
		if err := saveServerStates(); err != nil {
			s.Log().WithField("error", err).Warn("failed to write server states to disk")
		}
	}()

	// If server was in an online state, and is now in an offline state we should handle
	// that as a crash event. In that scenario, check the last crash time, and the crash
	// counter.
	//
	// In the event that we have passed the thresholds, don't do anything, otherwise
	// automatically attempt to start the process back up for the user. This is done in a
	// separate thread as to not block any actions currently taking place in the flow
	// that called this function.
	if (prevState == ProcessStartingState || prevState == ProcessRunningState) && s.GetState() == ProcessOfflineState {
		s.Log().Info("detected server as entering a crashed state; running crash handler")

		go func(server *Server) {
			if err := server.handleServerCrash(); err != nil {
				if IsTooFrequentCrashError(err) {
					server.Log().Info("did not restart server after crash; occurred too soon after the last")
				} else {
					server.Log().WithField("error", err).Error("failed to handle server crash")
				}
			}
		}(s)
	}

	return nil
}

// Returns the current state of the server in a race-safe manner.
func (s *Server) GetState() string {
	return s.Proc().State
}

// Determines if the server state is running or not. This is different than the
// environment state, it is simply the tracked state from this daemon instance, and
// not the response from Docker.
func (s *Server) IsRunning() bool {
	return s.GetState() == ProcessRunningState || s.GetState() == ProcessStartingState
}
