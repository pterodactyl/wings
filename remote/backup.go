package remote

import (
	"context"
	"fmt"
	"strconv"

	"github.com/pterodactyl/wings/api"
)

func (c *client) GetBackupRemoteUploadURLs(ctx context.Context, backup string, size int64) (api.BackupRemoteUploadResponse, error) {
	res, err := c.get(ctx, fmt.Sprintf("/backups/%s", backup), q{"size": strconv.FormatInt(size, 10)})
	if err != nil {
		return api.BackupRemoteUploadResponse{}, err
	}
	defer res.Body.Close()

	if res.HasError() {
		return api.BackupRemoteUploadResponse{}, res.Error()
	}

	r := api.BackupRemoteUploadResponse{}
	err = res.BindJSON(&r)
	return r, err
}

func (c *client) SetBackupStatus(ctx context.Context, backup string, data api.BackupRequest) error {
	resp, err := c.post(ctx, fmt.Sprintf("/backups/%s", backup), data)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return resp.Error()
}
