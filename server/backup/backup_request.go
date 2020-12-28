package backup

import (
	"errors"
	"fmt"
)

type Request struct {
	Adapter AdapterType `json:"adapter"`
	Uuid    string      `json:"uuid"`
	Ignore  string      `json:"ignore"`
}

// Generates a new local backup struct.
func (r *Request) NewLocalBackup() (*LocalBackup, error) {
	if r.Adapter != LocalBackupAdapter {
		return nil, errors.New(fmt.Sprintf("cannot create local backup using [%s] adapter", r.Adapter))
	}

	return &LocalBackup{
		Backup{
			Uuid:    r.Uuid,
			Ignore:  r.Ignore,
			adapter: LocalBackupAdapter,
		},
	}, nil
}

// Generates a new S3 backup struct.
func (r *Request) NewS3Backup() (*S3Backup, error) {
	if r.Adapter != S3BackupAdapter {
		return nil, errors.New(fmt.Sprintf("cannot create s3 backup using [%s] adapter", r.Adapter))
	}

	return &S3Backup{
		Backup: Backup{
			Uuid:    r.Uuid,
			Ignore:  r.Ignore,
			adapter: S3BackupAdapter,
		},
	}, nil
}
