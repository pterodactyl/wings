package server

import (
	"encoding/json"
	"github.com/pkg/errors"
	"io/ioutil"
	"os"
	"sync"
)

var (
	statesLock sync.Mutex
	statesFile = "data/states.json"
)

// DoesStatesFileExist .
func DoesStatesFileExist() (bool, error) {
	statesLock.Lock()
	defer statesLock.Unlock()

	if _, err := os.Stat(statesFile); err != nil {
		if !os.IsNotExist(err) {
			return false, errors.WithStack(err)
		}

		return false, nil
	}

	return true, nil
}

// FetchServerStates .
func FetchServerStates() (map[string]string, error) {
	// Check if the states file exists.
	exists, err := DoesStatesFileExist()
	if err != nil {
		return nil, errors.WithStack(err)
	}

	// Request a lock after we check if the file exists.
	statesLock.Lock()
	defer statesLock.Unlock()

	// Return an empty map if the file does not exist.
	if !exists {
		return map[string]string{}, nil
	}

	// Open the states file.
	f, err := os.Open(statesFile)
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

	statesLock.Lock()
	defer statesLock.Unlock()

	// Write the data to the file
	if err := ioutil.WriteFile(statesFile, data, 0644); err != nil {
		return errors.WithStack(err)
	}

	return nil
}
