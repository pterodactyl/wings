package backup

import (
	"crypto/sha1"
	"encoding/hex"
	"io"
	"os"
	"path"
	"sync"

	"github.com/apex/log"
	"github.com/pterodactyl/wings/api"
	"github.com/pterodactyl/wings/config"
)

type AdapterType string

const (
	LocalBackupAdapter AdapterType = "wings"
	S3BackupAdapter    AdapterType = "s3"
)

// RestoreCallback is a generic restoration callback that exists for both local
// and remote backups allowing the files to be restored.
type RestoreCallback func(file string, r io.Reader) error

type ArchiveDetails struct {
	Checksum     string `json:"checksum"`
	ChecksumType string `json:"checksum_type"`
	Size         int64  `json:"size"`
}

// ToRequest returns a request object.
func (ad *ArchiveDetails) ToRequest(successful bool) api.BackupRequest {
	return api.BackupRequest{
		Checksum:     ad.Checksum,
		ChecksumType: ad.ChecksumType,
		Size:         ad.Size,
		Successful:   successful,
	}
}

type Backup struct {
	// The UUID of this backup object. This must line up with a backup from
	// the panel instance.
	Uuid string `json:"uuid"`

	// An array of files to ignore when generating this backup. This should be
	// compatible with a standard .gitignore structure.
	Ignore string `json:"ignore"`

	adapter    AdapterType
	logContext map[string]interface{}
}

// noinspection GoNameStartsWithPackageName
type BackupInterface interface {
	// Identifier returns the UUID of this backup as tracked by the panel
	// instance.
	Identifier() string
	// WithLogContext attaches additional context to the log output for this
	// backup.
	WithLogContext(map[string]interface{})
	// Generate creates a backup in whatever the configured source for the
	// specific implementation is.
	Generate(string, string) (*ArchiveDetails, error)
	// Ignored returns the ignored files for this backup instance.
	Ignored() string
	// Checksum returns a SHA1 checksum for the generated backup.
	Checksum() ([]byte, error)
	// Size returns the size of the generated backup.
	Size() (int64, error)
	// Path returns the path to the backup on the machine. This is not always
	// the final storage location of the backup, simply the location we're using
	// to store it until it is moved to the final spot.
	Path() string
	// Details returns details about the archive.
	Details() *ArchiveDetails
	// Remove removes a backup file.
	Remove() error
	// Restore is called when a backup is ready to be restored to the disk from
	// the given source. Not every backup implementation will support this nor
	// will every implementation require a reader be provided.
	Restore(reader io.Reader, callback RestoreCallback) error
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
		return 0, err
	}

	return st.Size(), nil
}

// Returns the SHA256 checksum of a backup.
func (b *Backup) Checksum() ([]byte, error) {
	h := sha1.New()

	f, err := os.Open(b.Path())
	if err != nil {
		return nil, err
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

	l := log.WithField("backup_id", b.Uuid)

	var checksum string
	// Calculate the checksum for the file.
	go func() {
		defer wg.Done()

		l.Info("computing checksum for backup...")
		resp, err := b.Checksum()
		if err != nil {
			log.WithFields(log.Fields{
				"backup": b.Identifier(),
				"error":  err,
			}).Error("failed to calculate checksum for backup")
			return
		}

		checksum = hex.EncodeToString(resp)
		l.WithField("checksum", checksum).Info("computed checksum for backup")
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
		Checksum:     checksum,
		ChecksumType: "sha1",
		Size:         sz,
	}
}

func (b *Backup) Ignored() string {
	return b.Ignore
}

// Returns a logger instance for this backup with the additional context fields
// assigned to the output.
func (b *Backup) log() *log.Entry {
	l := log.WithField("backup", b.Identifier()).WithField("adapter", b.adapter)
	for k, v := range b.logContext {
		l = l.WithField(k, v)
	}
	return l
}
