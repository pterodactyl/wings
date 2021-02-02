package api

import (
	"encoding/json"
	"fmt"
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

// Defines installation script information for a server process. This is used when
// a server is installed for the first time, and when a server is marked for re-installation.
type InstallationScript struct {
	ContainerImage string `json:"container_image"`
	Entrypoint     string `json:"entrypoint"`
	Script         string `json:"script"`
}

type RawServerData struct {
	Uuid                 string          `json:"uuid"`
	Settings             json.RawMessage `json:"settings"`
	ProcessConfiguration json.RawMessage `json:"process_configuration"`
}

// Fetches the server configuration and returns the struct for it.
func (r *Request) GetServerConfiguration(uuid string) (ServerConfigurationResponse, error) {
	var cfg ServerConfigurationResponse

	resp, err := r.Get(fmt.Sprintf("/servers/%s", uuid), nil)
	if err != nil {
		return cfg, err
	}
	defer resp.Body.Close()

	if resp.HasError() {
		return cfg, resp.Error()
	}

	if err := resp.Bind(&cfg); err != nil {
		return cfg, err
	}

	return cfg, nil
}

// Fetches installation information for the server process.
func (r *Request) GetInstallationScript(uuid string) (InstallationScript, error) {
	var is InstallationScript
	resp, err := r.Get(fmt.Sprintf("/servers/%s/install", uuid), nil)
	if err != nil {
		return is, err
	}
	defer resp.Body.Close()

	if resp.HasError() {
		return is, resp.Error()
	}

	if err := resp.Bind(&is); err != nil {
		return is, err
	}

	return is, nil
}

// Marks a server as being installed successfully or unsuccessfully on the panel.
func (r *Request) SendInstallationStatus(uuid string, successful bool) error {
	resp, err := r.Post(fmt.Sprintf("/servers/%s/install", uuid), D{"successful": successful})
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.HasError() {
		return resp.Error()
	}

	return nil
}

func (r *Request) SendArchiveStatus(uuid string, successful bool) error {
	resp, err := r.Post(fmt.Sprintf("/servers/%s/archive", uuid), D{"successful": successful})
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	return resp.Error()
}

func (r *Request) SendTransferStatus(uuid string, successful bool) error {
	state := "failure"
	if successful {
		state = "success"
	}
	resp, err := r.Get(fmt.Sprintf("/servers/%s/transfer/%s", uuid, state), nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return resp.Error()
}
