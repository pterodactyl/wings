package remote

import (
	"context"
	"fmt"
	"strconv"
)

func (c *client) GetBackupRemoteUploadURLs(ctx context.Context, backup string, size int64) (BackupRemoteUploadResponse, error) {
	var data BackupRemoteUploadResponse
	res, err := c.get(ctx, fmt.Sprintf("/backups/%s", backup), q{"size": strconv.FormatInt(size, 10)})
	if err != nil {
		return data, err
	}
	defer res.Body.Close()

	if res.HasError() {
		return data, res.Error()
	}

	err = res.BindJSON(&data)
	return data, err
}

func (c *client) SetBackupStatus(ctx context.Context, backup string, data BackupRequest) error {
	resp, err := c.post(ctx, fmt.Sprintf("/backups/%s", backup), data)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return resp.Error()
}


// SendRestorationStatus triggers a request to the Panel to notify it that a
// restoration has been completed and the server should be marked as being
// activated again.
func (c *client) SendRestorationStatus(ctx context.Context, backup string, successful bool) error {
	resp, err := c.post(ctx, fmt.Sprintf("/backups/%s/restore", backup), d{"successful": successful})
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return resp.Error()
}