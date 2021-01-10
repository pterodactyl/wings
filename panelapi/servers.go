package panelapi

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"sync"

	"github.com/pterodactyl/wings/api"
	"golang.org/x/sync/errgroup"
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
	Settings             json.RawMessage           `json:"settings"`
	ProcessConfiguration *api.ProcessConfiguration `json:"process_configuration"`
}

// Defines installation script information for a server process. This is used when
// a server is installed for the first time, and when a server is marked for re-installation.
type InstallationScript struct {
	ContainerImage string `json:"container_image"`
	Entrypoint     string `json:"entrypoint"`
	Script         string `json:"script"`
}

type allServerResponse struct {
	Data []api.RawServerData `json:"data"`
	Meta api.Pagination      `json:"meta"`
}

type RawServerData struct {
	Uuid                 string          `json:"uuid"`
	Settings             json.RawMessage `json:"settings"`
	ProcessConfiguration json.RawMessage `json:"process_configuration"`
}

func (c *client) GetServersPaged(ctx context.Context, page, limit int) ([]api.RawServerData, api.Pagination, error) {
	res, err := c.get(ctx, "/servers", q{
		"page":     strconv.Itoa(page),
		"per_page": strconv.Itoa(limit),
	})
	if err != nil {
		return nil, api.Pagination{}, err
	}
	defer res.Body.Close()

	if res.HasError() {
		return nil, api.Pagination{}, res.Error()
	}

	var r allServerResponse
	if err := res.BindJSON(&r); err != nil {
		return nil, api.Pagination{}, err
	}

	return r.Data, r.Meta, nil
}

func (c *client) GetServers(ctx context.Context, perPage int) ([]api.RawServerData, error) {
	servers, pageMeta, err := c.GetServersPaged(ctx, 0, perPage)
	if err != nil {
		return nil, err
	}

	// if the amount of servers exceeds the page limit, get the remaining pages in parallel
	if pageMeta.LastPage > 1 {
		eg, _ := errgroup.WithContext(ctx)
		serversMu := sync.Mutex{}

		for page := pageMeta.CurrentPage + 1; page <= pageMeta.LastPage; page++ {
			eg.Go(func() error {
				ps, _, err := c.GetServersPaged(ctx, perPage, int(page))
				if err != nil {
					return err
				}
				serversMu.Lock()
				servers = append(servers, ps...)
				serversMu.Unlock()
				return nil
			})
		}

		if err := eg.Wait(); err != nil {
			return nil, err
		}
	}

	return servers, nil
}

func (c *client) GetServerConfiguration(ctx context.Context, uuid string) (api.ServerConfigurationResponse, error) {
	res, err := c.get(ctx, fmt.Sprintf("/servers/%s", uuid), nil)
	if err != nil {
		return api.ServerConfigurationResponse{}, err
	}
	defer res.Body.Close()

	if res.HasError() {
		return api.ServerConfigurationResponse{}, err
	}

	config := api.ServerConfigurationResponse{}
	err = res.BindJSON(&config)
	return config, err
}

func (c *client) GetInstallationScript(ctx context.Context, uuid string) (api.InstallationScript, error) {
	res, err := c.get(ctx, fmt.Sprintf("/servers/%s/install", uuid), nil)
	if err != nil {
		return api.InstallationScript{}, err
	}
	defer res.Body.Close()

	if res.HasError() {
		return api.InstallationScript{}, err
	}

	config := api.InstallationScript{}
	err = res.BindJSON(&config)
	return config, err
}

func (c *client) SetInstallationStatus(ctx context.Context, uuid string, successful bool) error {
	resp, err := c.post(ctx, fmt.Sprintf("/servers/%s/install", uuid), d{"successful": successful})
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return resp.Error()
}

func (c *client) SetArchiveStatus(ctx context.Context, uuid string, successful bool) error {
	resp, err := c.post(ctx, fmt.Sprintf("/servers/%s/archive", uuid), d{"successful": successful})
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return resp.Error()
}

func (c *client) SetTransferStatus(ctx context.Context, uuid string, successful bool) error {
	state := "failure"
	if successful {
		state = "success"
	}
	resp, err := c.get(ctx, fmt.Sprintf("/servers/%s/transfer/%s", uuid, state), nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return resp.Error()
}
