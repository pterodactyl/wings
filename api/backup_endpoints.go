package api

import (
	"fmt"
	"strconv"
)

type BackupRemoteUploadResponse struct {
	Parts    []string `json:"parts"`
	PartSize int64    `json:"part_size"`
}

func (r *Request) GetBackupRemoteUploadURLs(backup string, size int64) (*BackupRemoteUploadResponse, error) {
	resp, err := r.Get(fmt.Sprintf("/backups/%s", backup), Q{"size": strconv.FormatInt(size, 10)})
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.HasError() {
		return nil, resp.Error()
	}

	var res BackupRemoteUploadResponse
	if err := resp.Bind(&res); err != nil {
		return nil, err
	}

	return &res, nil
}

type BackupRequest struct {
	Checksum     string `json:"checksum"`
	ChecksumType string `json:"checksum_type"`
	Size         int64  `json:"size"`
	Successful   bool   `json:"successful"`
}

// Notifies the panel that a specific backup has been completed and is now
// available for a user to view and download.
func (r *Request) SendBackupStatus(backup string, data BackupRequest) error {
	resp, err := r.Post(fmt.Sprintf("/backups/%s", backup), data)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	return resp.Error()
}
