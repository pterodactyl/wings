package server

import (
	"encoding/json"
	"github.com/pkg/errors"
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

// SaveServerStates .
func SaveServerStates() error {
	// Get the states of all servers on the daemon.
	states := map[string]string{}
	for _, s := range GetServers().All() {
		states[s.Uuid] = s.State
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
