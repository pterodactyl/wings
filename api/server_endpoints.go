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
		Done            []string `json:"done"`
		UserInteraction []string `json:"user_interaction"`
		StripAnsi       bool     `json:"strip_ansi"`
	} `json:"startup"`

	Stop struct {
		Type  string `json:"type"`
		Value string `json:"value"`
	} `json:"stop"`

	ConfigurationFiles []parser.ConfigurationFile `json:"configs"`
}

// Defines installation script information for a server process. This is used when
// a server is installed for the first time, and when a server is marked for re-installation.
type InstallationScript struct {
	ContainerImage string `json:"container_image"`
	Entrypoint     string `json:"entrypoint"`
	Script         string `json:"script"`
}

// GetAllServerConfigurations fetches configurations for all servers assigned to this node.
func (r *PanelRequest) GetAllServerConfigurations() (map[string]*ServerConfigurationResponse, *RequestError, error) {
	resp, err := r.Get("/servers")
	if err != nil {
		return nil, nil, errors.WithStack(err)
	}
	defer resp.Body.Close()

	r.Response = resp

	if r.HasError() {
		return nil, r.Error(), nil
	}

	b, _ := r.ReadBody()
	res := map[string]*ServerConfigurationResponse{}
	if len(b) == 2 {
		return res, nil, nil
	}

	if err := json.Unmarshal(b, &res); err != nil {
		return nil, nil, errors.WithStack(err)
	}

	return res, nil, nil
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

// Fetches installation information for the server process.
func (r *PanelRequest) GetInstallationScript(uuid string) (InstallationScript, *RequestError, error) {
	res := InstallationScript{}

	resp, err := r.Get(fmt.Sprintf("/servers/%s/install", uuid))
	if err != nil {
		return res, nil, errors.WithStack(err)
	}
	defer resp.Body.Close()

	r.Response = resp

	if r.HasError() {
		return res, r.Error(), nil
	}

	b, _ := r.ReadBody()

	if err := json.Unmarshal(b, &res); err != nil {
		return res, nil, errors.WithStack(err)
	}

	return res, nil, nil
}

type installRequest struct {
	Successful bool `json:"successful"`
}

// Marks a server as being installed successfully or unsuccessfully on the panel.
func (r *PanelRequest) SendInstallationStatus(uuid string, successful bool) (*RequestError, error) {
	b, err := json.Marshal(installRequest{Successful: successful})
	if err != nil {
		return nil, errors.WithStack(err)
	}

	resp, err := r.Post(fmt.Sprintf("/servers/%s/install", uuid), b)
	if err != nil {
		return nil, errors.WithStack(err)
	}
	defer resp.Body.Close()

	r.Response = resp
	if r.HasError() {
		return r.Error(), nil
	}

	return nil, nil
}

type archiveRequest struct {
	Successful bool `json:"successful"`
}

func (r *PanelRequest) SendArchiveStatus(uuid string, successful bool) (*RequestError, error) {
	b, err := json.Marshal(archiveRequest{Successful: successful})
	if err != nil {
		return nil, errors.WithStack(err)
	}

	resp, err := r.Post(fmt.Sprintf("/servers/%s/archive", uuid), b)
	if err != nil {
		return nil, errors.WithStack(err)
	}
	defer resp.Body.Close()

	r.Response = resp
	if r.HasError() {
		return r.Error(), nil
	}

	return nil, nil
}

func (r *PanelRequest) SendTransferFailure(uuid string) (*RequestError, error) {
	resp, err := r.Get(fmt.Sprintf("/servers/%s/transfer/failure", uuid))
	if err != nil {
		return nil, errors.WithStack(err)
	}
	defer resp.Body.Close()

	r.Response = resp
	if r.HasError() {
		return r.Error(), nil
	}

	return nil, nil
}

func (r *PanelRequest) SendTransferSuccess(uuid string) (*RequestError, error) {
	resp, err := r.Get(fmt.Sprintf("/servers/%s/transfer/success", uuid))
	if err != nil {
		return nil, errors.WithStack(err)
	}
	defer resp.Body.Close()

	r.Response = resp
	if r.HasError() {
		return r.Error(), nil
	}

	return nil, nil
}
