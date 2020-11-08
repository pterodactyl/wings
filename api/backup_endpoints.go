package api

import (
	"emperror.dev/errors"
	"fmt"
	"strconv"
)

type BackupRemoteUploadResponse struct {
	CompleteMultipartUpload string   `json:"complete_multipart_upload"`
	AbortMultipartUpload    string   `json:"abort_multipart_upload"`
	Parts                   []string `json:"parts"`
	PartSize                int64    `json:"part_size"`
}

func (r *Request) GetBackupRemoteUploadURLs(backup string, size int64) (*BackupRemoteUploadResponse, error) {
	resp, err := r.Get(fmt.Sprintf("/backups/%s", backup), Q{"size": strconv.FormatInt(size, 10)})
	if err != nil {
		return nil, errors.WithStackIf(err)
	}
	defer resp.Body.Close()

	if resp.HasError() {
		return nil, resp.Error()
	}

	var res BackupRemoteUploadResponse
	if err := resp.Bind(&res); err != nil {
		return nil, errors.WithStackIf(err)
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
		return errors.WithStackIf(err)
	}
	defer resp.Body.Close()

	return resp.Error()
}
