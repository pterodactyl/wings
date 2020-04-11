package server

import (
	"encoding/json"
	"fmt"
	"github.com/pkg/errors"
	"go.uber.org/zap"
	"io/ioutil"
	"os"
	"sync"
)

const stateFileLocation = "data/.states.json"

var stateMutex sync.Mutex

// Checks if the state tracking file exists, if not it is generated.
func ensureStateFileExists() (bool, error) {
	stateMutex.Lock()
	defer stateMutex.Unlock()

	if _, err := os.Stat(stateFileLocation); err != nil {
		if !os.IsNotExist(err) {
			return false, errors.WithStack(err)
		}

		return false, nil
	}

	return true, nil
}

// Returns the state of the servers.
func getServerStates() (map[string]string, error) {
	// Check if the states file exists.
	exists, err := ensureStateFileExists()
	if err != nil {
		return nil, errors.WithStack(err)
	}

	// Request a lock after we check if the file exists.
	stateMutex.Lock()
	defer stateMutex.Unlock()

	// Return an empty map if the file does not exist.
	if !exists {
		return map[string]string{}, nil
	}

	// Open the states file.
	f, err := os.Open(stateFileLocation)
	if err != nil {
		return nil, errors.WithStack(err)
	}
	defer f.Close()

	// Convert the json object to a map.
	states := map[string]string{}
	if err := json.NewDecoder(f).Decode(&states); err != nil {
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
	if err := ioutil.WriteFile(stateFileLocation, data, 0644); err != nil {
		return errors.WithStack(err)
	}

	return nil
}

const (
	ProcessOfflineState  = "offline"
	ProcessStartingState = "starting"
	ProcessRunningState  = "running"
	ProcessStoppingState = "stopping"
)

// Sets the state of the server internally. This function handles crash detection as
// well as reporting to event listeners for the server.
func (s *Server) SetState(state string) error {
	if state != ProcessOfflineState && state != ProcessStartingState && state != ProcessRunningState && state != ProcessStoppingState {
		return errors.New(fmt.Sprintf("invalid server state received: %s", state))
	}

	prevState := s.GetState()

	// Obtain a mutex lock and update the current state of the server.
	s.Lock()
	s.State = state

	// Emit the event to any listeners that are currently registered.
	zap.S().Debugw("saw server status change event", zap.String("server", s.Uuid), zap.String("status", s.State))
	s.Events().Publish(StatusEvent, s.State)

	// Release the lock as it is no longer needed for the following actions.
	s.Unlock()

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
			zap.S().Warnw("failed to write server states to disk", zap.Error(err))
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
		zap.S().Infow("detected server as entering a potentially crashed state; running handler", zap.String("server", s.Uuid))

		go func(server *Server) {
			if err := server.handleServerCrash(); err != nil {
				if IsTooFrequentCrashError(err) {
					zap.S().Infow("did not restart server after crash; occurred too soon after last", zap.String("server", server.Uuid))
				} else {
					zap.S().Errorw("failed to handle server crash state", zap.String("server", server.Uuid), zap.Error(err))
				}
			}
		}(s)
	}

	return nil
}

// Returns the current state of the server in a race-safe manner.
func (s *Server) GetState() string {
	s.RLock()
	defer s.RUnlock()

	return s.State
}

// Determines if the server state is running or not. This is different than the
// environment state, it is simply the tracked state from this daemon instance, and
// not the response from Docker.
func (s *Server) IsRunning() bool {
	return s.GetState() == ProcessRunningState || s.GetState() == ProcessStartingState
}