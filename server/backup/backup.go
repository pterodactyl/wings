package backup

import (
	"crypto/sha256"
	"encoding/hex"
	"github.com/apex/log"
	"github.com/pkg/errors"
	"github.com/pterodactyl/wings/api"
	"github.com/pterodactyl/wings/config"
	"io"
	"os"
	"path"
	"sync"
)

const (
	LocalBackupAdapter = "wings"
	S3BackupAdapter    = "s3"
)

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

type Backup struct {
	// The UUID of this backup object. This must line up with a backup from
	// the panel instance.
	Uuid string `json:"uuid"`

	// An array of files to ignore when generating this backup. This should be
	// compatible with a standard .gitignore structure.
	IgnoredFiles []string `json:"ignored_files"`
}

// noinspection GoNameStartsWithPackageName
type BackupInterface interface {
	// Returns the UUID of this backup as tracked by the panel instance.
	Identifier() string

	// Generates a backup in whatever the configured source for the specific
	// implementation is.
	Generate(*IncludedFiles, string) (*ArchiveDetails, error)

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

func (b *Backup) Identifier() string {
	return b.Uuid
}

// Returns the path for this specific backup.
func (b *Backup) Path() string {
	return path.Join(config.Get().System.BackupDirectory, b.Identifier()+".tar.gz")
}

// Return the size of the generated backup.
func (b *Backup) Size() (int64, error) {
	st, err := os.Stat(b.Path())
	if err != nil {
		return 0, errors.WithStack(err)
	}

	return st.Size(), nil
}

// Returns the SHA256 checksum of a backup.
func (b *Backup) Checksum() ([]byte, error) {
	h := sha256.New()

	f, err := os.Open(b.Path())
	if err != nil {
		return nil, errors.WithStack(err)
	}
	defer f.Close()

	buf := make([]byte, 1024*4)
	if _, err := io.CopyBuffer(h, f, buf); err != nil {
		return nil, err
	}

	return h.Sum(nil), nil
}

// Returns details of the archive by utilizing two go-routines to get the checksum and
// the size of the archive.
func (b *Backup) Details() *ArchiveDetails {
	wg := sync.WaitGroup{}
	wg.Add(2)

	var checksum string
	// Calculate the checksum for the file.
	go func() {
		defer wg.Done()

		resp, err := b.Checksum()
		if err != nil {
			log.WithFields(log.Fields{
				"backup": b.Identifier(),
				"error":  err,
			}).Error("failed to calculate checksum for backup")
		}

		checksum = hex.EncodeToString(resp)
	}()

	var sz int64
	go func() {
		defer wg.Done()

		if s, err := b.Size(); err != nil {
			return
		} else {
			sz = s
		}
	}()

	wg.Wait()

	return &ArchiveDetails{
		Checksum: checksum,
		Size:     sz,
	}
}

func (b *Backup) Ignored() []string {
	return b.IgnoredFiles
}
