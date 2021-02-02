package remote

import (
	"context"
	"fmt"
	"strconv"
	"sync"

	"emperror.dev/errors"
	"github.com/apex/log"
	"golang.org/x/sync/errgroup"
)

const (
	ProcessStopCommand    = "command"
	ProcessStopSignal     = "signal"
	ProcessStopNativeStop = "stop"
)

// GetServers returns all of the servers that are present on the Panel making
// parallel API calls to the endpoint if more than one page of servers is
// returned.
func (c *client) GetServers(ctx context.Context, limit int) ([]RawServerData, error) {
	servers, meta, err := c.getServersPaged(ctx, 0, limit)
	if err != nil {
		return nil, err
	}

	var mu sync.Mutex
	if meta.LastPage > 1 {
		g, ctx := errgroup.WithContext(ctx)
		for page := meta.CurrentPage + 1; page <= meta.LastPage; page++ {
			page := page
			g.Go(func() error {
				ps, _, err := c.getServersPaged(ctx, int(page), limit)
				if err != nil {
					return err
				}
				mu.Lock()
				servers = append(servers, ps...)
				mu.Unlock()
				return nil
			})
		}
		if err := g.Wait(); err != nil {
			return nil, err
		}
	}

	return servers, nil
}

func (c *client) GetServerConfiguration(ctx context.Context, uuid string) (ServerConfigurationResponse, error) {
	var config ServerConfigurationResponse
	res, err := c.get(ctx, fmt.Sprintf("/servers/%s", uuid), nil)
	if err != nil {
		return config, err
	}
	defer res.Body.Close()

	if res.HasError() {
		return config, err
	}

	err = res.BindJSON(&config)
	return config, err
}

func (c *client) GetInstallationScript(ctx context.Context, uuid string) (InstallationScript, error) {
	res, err := c.get(ctx, fmt.Sprintf("/servers/%s/install", uuid), nil)
	if err != nil {
		return InstallationScript{}, err
	}
	defer res.Body.Close()

	if res.HasError() {
		return InstallationScript{}, err
	}

	var config InstallationScript
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

// ValidateSftpCredentials makes a request to determine if the username and
// password combination provided is associated with a valid server on the instance
// using the Panel's authentication control mechanisms. This will get itself
// throttled if too many requests are made, allowing us to completely offload
// all of the authorization security logic to the Panel.
func (c *client) ValidateSftpCredentials(ctx context.Context, request SftpAuthRequest) (SftpAuthResponse, error) {
	var auth SftpAuthResponse
	res, err := c.post(ctx, "/sftp/auth", request)
	if err != nil {
		return auth, err
	}

	e := res.Error()
	if e != nil {
		if res.StatusCode >= 400 && res.StatusCode < 500 {
			log.WithFields(log.Fields{
				"subsystem": "sftp",
				"username":  request.User,
				"ip":        request.IP,
			}).Warn(e.Error())

			return auth, &SftpInvalidCredentialsError{}
		}

		return auth, errors.New(e.Error())
	}

	err = res.BindJSON(&auth)
	return auth, err
}

// getServersPaged returns a subset of servers from the Panel API using the
// pagination query parameters.
func (c *client) getServersPaged(ctx context.Context, page, limit int) ([]RawServerData, Pagination, error) {
	var r struct {
		Data []RawServerData `json:"data"`
		Meta Pagination      `json:"meta"`
	}

	res, err := c.get(ctx, "/servers", q{
		"page":     strconv.Itoa(page),
		"per_page": strconv.Itoa(limit),
	})
	if err != nil {
		return nil, r.Meta, err
	}
	defer res.Body.Close()

	if res.HasError() {
		return nil, r.Meta, res.Error()
	}
	if err := res.BindJSON(&r); err != nil {
		return nil, r.Meta, err
	}
	return r.Data, r.Meta, nil
}