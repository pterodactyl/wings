package remote

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/pterodactyl/wings/api"
)

type Client interface {
	GetBackupRemoteUploadURLs(ctx context.Context, backup string, size int64) (api.BackupRemoteUploadResponse, error)
	GetInstallationScript(ctx context.Context, uuid string) (api.InstallationScript, error)
	GetServerConfiguration(ctx context.Context, uuid string) (api.ServerConfigurationResponse, error)
	GetServers(context context.Context, perPage int) ([]api.RawServerData, error)
	SetArchiveStatus(ctx context.Context, uuid string, successful bool) error
	SetBackupStatus(ctx context.Context, backup string, data api.BackupRequest) error
	SetInstallationStatus(ctx context.Context, uuid string, successful bool) error
	SetTransferStatus(ctx context.Context, uuid string, successful bool) error
	ValidateSftpCredentials(ctx context.Context, request api.SftpAuthRequest) (api.SftpAuthResponse, error)
}

type client struct {
	httpClient *http.Client
	baseUrl    string
	tokenId    string
	token      string
	retries    int
}

type ClientOption func(c *client)

func CreateClient(base, tokenId, token string, opts ...ClientOption) Client {
	httpClient := &http.Client{
		Timeout: time.Second * 15,
	}
	base = strings.TrimSuffix(base, "/")
	c := &client{
		baseUrl:    base + "/api/remote",
		tokenId:    tokenId,
		token:      token,
		httpClient: httpClient,
		retries:    3,
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

func WithTimeout(timeout time.Duration) ClientOption {
	return func(c *client) {
		c.httpClient.Timeout = timeout
	}
}
