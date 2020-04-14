package api

import (
	"encoding/json"
	"fmt"
	"github.com/pkg/errors"
)

type BackupRequest struct {
	Checksum string `json:"checksum"`
	Size     int64  `json:"size"`
	Successful bool `json:"successful"`
}

// Notifies the panel that a specific backup has been completed and is now
// available for a user to view and download.
func (r *PanelRequest) SendBackupStatus(backup string, data BackupRequest) (*RequestError, error) {
	b, err := json.Marshal(data)
	if err != nil {
		return nil, errors.WithStack(err)
	}

	resp, err := r.Post(fmt.Sprintf("/backups/%s", backup), b)
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
