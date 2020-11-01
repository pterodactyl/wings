package backup

import (
	"fmt"
	"github.com/pkg/errors"
)

type Request struct {
	Adapter      string   `json:"adapter"`
	Uuid         string   `json:"uuid"`
	IgnoredFiles []string `json:"ignored_files"`
}

// Generates a new local backup struct.
func (r *Request) NewLocalBackup() (*LocalBackup, error) {
	if r.Adapter != LocalBackupAdapter {
		return nil, errors.New(fmt.Sprintf("cannot create local backup using [%s] adapter", r.Adapter))
	}

	return &LocalBackup{
		Backup{
			Uuid:         r.Uuid,
			IgnoredFiles: r.IgnoredFiles,
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
			Uuid:         r.Uuid,
			IgnoredFiles: r.IgnoredFiles,
		},
	}, nil
}
