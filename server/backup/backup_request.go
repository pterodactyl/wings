package backup

import (
	"fmt"
	"github.com/pkg/errors"
)

type Request struct {
	Adapter      string   `json:"adapter"`
	Uuid         string   `json:"uuid"`
	IgnoredFiles []string `json:"ignored_files"`
	PresignedUrl string   `json:"presigned_url"`
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

	if len(r.PresignedUrl) == 0 {
		return nil, errors.New("a valid presigned S3 upload URL must be provided to use the [s3] adapter")
	}

	return &S3Backup{
		Backup: Backup{
			Uuid:         r.Uuid,
			IgnoredFiles: r.IgnoredFiles,
		},
		PresignedUrl: r.PresignedUrl,
	}, nil
}
