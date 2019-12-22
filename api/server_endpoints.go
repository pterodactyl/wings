package api

import (
	"encoding/json"
	"fmt"
	"github.com/pkg/errors"
	"github.com/pterodactyl/wings/parser"
)

const (
	ProcessStopCommand    = "command"
	ProcessStopSignal     = "signal"
	ProcessStopNativeStop = "stop"
)

// Holds the server configuration data returned from the Panel. When a server process
// is started, Wings communicates with the Panel to fetch the latest build information
// as well as get all of the details needed to parse the given Egg.
//
// This means we do not need to hit Wings each time part of the server is updated, and
// the Panel serves as the source of truth at all times. This also means if a configuration
// is accidentally wiped on Wings we can self-recover without too much hassle, so long
// as Wings is aware of what servers should exist on it.
type ServerConfigurationResponse struct {
	Settings             json.RawMessage       `json:"settings"`
	ProcessConfiguration *ProcessConfiguration `json:"process_configuration"`
}

// Defines the process configuration for a given server instance. This sets what the
// daemon is looking for to mark a server as done starting, what to do when stopping,
// and what changes to make to the configuration file for a server.
type ProcessConfiguration struct {
	Startup struct {
		Done            string   `json:"done"`
		UserInteraction []string `json:"userInteraction"`
	} `json:"startup"`
	Stop struct {
		Type  string `json:"type"`
		Value string `json:"value"`
	} `json:"stop"`
	ConfigurationFiles []parser.ConfigurationFile `json:"configs"`
}

// Fetches the server configuration and returns the struct for it.
func (r *PanelRequest) GetServerConfiguration(uuid string) (*ServerConfigurationResponse, *RequestError, error) {
	resp, err := r.Get(fmt.Sprintf("/servers/%s", uuid))
	if err != nil {
		return nil, nil, errors.WithStack(err)
	}
	defer resp.Body.Close()

	r.Response = resp

	if r.HasError() {
		return nil, r.Error(), nil
	}

	res := &ServerConfigurationResponse{}
	b, _ := r.ReadBody()

	if err := json.Unmarshal(b, res); err != nil {
		return nil, nil, errors.WithStack(err)
	}

	return res, nil, nil
}
