package backup

import (
	"github.com/pterodactyl/wings/api"
)

type Backup interface {
	// Returns the UUID of this backup as tracked by the panel instance.
	Identifier() string

	// Generates a backup in whatever the configured source for the specific
	// implementation is.
	Backup(dir string) error

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