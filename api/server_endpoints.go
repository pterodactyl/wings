package api

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/apex/log"
	"github.com/pterodactyl/wings/config"
	"golang.org/x/sync/errgroup"
	"strconv"
	"sync"
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

type allServerResponse struct {
	Data []RawServerData `json:"data"`
	Meta Pagination      `json:"meta"`
}

type RawServerData struct {
	Uuid                 string          `json:"uuid"`
	Settings             json.RawMessage `json:"settings"`
	ProcessConfiguration json.RawMessage `json:"process_configuration"`
}

// Fetches all of the server configurations from the Panel API. This will initially load the
// first 50 servers, and then check the pagination response to determine if more pages should
// be loaded. If so, those requests are spun-up in additional routines and the final resulting
// slice of all servers will be returned.
func (r *Request) GetServers() ([]RawServerData, error) {
	resp, err := r.Get("/servers", Q{"per_page": strconv.Itoa(int(config.Get().RemoteQuery.BootServersPerPage))})
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.HasError() {
		return nil, resp.Error()
	}

	var res allServerResponse
	if err := resp.Bind(&res); err != nil {
		return nil, err
	}

	var mu sync.Mutex
	ret := res.Data

	// Check for pagination, and if it exists we'll need to then make a request to the API
	// for each page that would exist and get all of the resulting servers.
	if res.Meta.LastPage > 1 {
		pp := res.Meta.PerPage
		log.WithField("per_page", pp).
			WithField("total_pages", res.Meta.LastPage).
			Debug("detected multiple pages of server configurations, fetching remaining...")

		g, ctx := errgroup.WithContext(context.Background())
		for i := res.Meta.CurrentPage + 1; i <= res.Meta.LastPage; i++ {
			page := strconv.Itoa(int(i))

			g.Go(func() error {
				select {
				case <-ctx.Done():
					return ctx.Err()
				default:
					{
						resp, err := r.Get("/servers", Q{"page": page, "per_page": strconv.Itoa(int(pp))})
						if err != nil {
							return err
						}
						defer resp.Body.Close()

						if resp.Error() != nil {
							return resp.Error()
						}

						var servers allServerResponse
						if err := resp.Bind(&servers); err != nil {
							return err
						}

						mu.Lock()
						defer mu.Unlock()
						ret = append(ret, servers.Data...)

						return nil
					}
				}
			})
		}

		if err := g.Wait(); err != nil {
			return nil, err
		}
	}

	return ret, nil
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
