package api

import (
	"fmt"
	"strconv"
	"sync"
)

var (
	backupUploadIDsMx sync.Mutex
	backupUploadIDs   = map[string]string{}
)

type BackupRemoteUploadResponse struct {
	UploadID string   `json:"upload_id"`
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

	// Store the backup upload id for later use, this is a janky way to be able to use it later with SendBackupStatus.
	backupUploadIDsMx.Lock()
	backupUploadIDs[backup] = res.UploadID
	backupUploadIDsMx.Unlock()

	return &res, nil
}

type BackupRequest struct {
	UploadID     string `json:"upload_id"`
	Checksum     string `json:"checksum"`
	ChecksumType string `json:"checksum_type"`
	Size         int64  `json:"size"`
	Successful   bool   `json:"successful"`
}

// Notifies the panel that a specific backup has been completed and is now
// available for a user to view and download.
func (r *Request) SendBackupStatus(backup string, data BackupRequest) error {
	// Set the UploadID on the data.
	backupUploadIDsMx.Lock()
	if v, ok := backupUploadIDs[backup]; ok {
		data.UploadID = v
	}
	backupUploadIDsMx.Unlock()

	resp, err := r.Post(fmt.Sprintf("/backups/%s", backup), data)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	return resp.Error()
}
