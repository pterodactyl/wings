package backup

import (
	"errors"
	"fmt"
	"github.com/pterodactyl/wings/api"
)

const (
	LocalBackupAdapter = "wings"
	S3BackupAdapter    = "s3"
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
		Uuid:         r.Uuid,
		IgnoredFiles: r.IgnoredFiles,
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
		Uuid:         r.Uuid,
		IgnoredFiles: r.IgnoredFiles,
		PresignedUrl: r.PresignedUrl,
	}, nil
}

type Backup interface {
	// Returns the UUID of this backup as tracked by the panel instance.
	Identifier() string

	// Generates a backup in whatever the configured source for the specific
	// implementation is.
	Backup(*IncludedFiles, string) error

	// Returns the ignored files for this backup instance.
	Ignored() []string

	// Returns a SHA256 checksum for the generated backup.
	Checksum() ([]byte, error)

	// Returns the size of the generated backup.
	Size() (int64, error)

	// Returns the path to the backup on the machine. This is not always the final
	// storage location of the backup, simply the location we're using to store
	// it until it is moved to the final spot.
	Path() string

	// Returns details about the archive.
	Details() *ArchiveDetails

	// Removes a backup file.
	Remove() error
}

type ArchiveDetails struct {
	Checksum string `json:"checksum"`
	Size     int64  `json:"size"`
}

// Returns a request object.
func (ad *ArchiveDetails) ToRequest(successful bool) api.BackupRequest {
	return api.BackupRequest{
		Checksum:   ad.Checksum,
		Size:       ad.Size,
		Successful: successful,
	}
}
