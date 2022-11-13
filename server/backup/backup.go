package backup

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"io"
	"io/fs"
	"os"
	"path"

	"emperror.dev/errors"
	"github.com/apex/log"
	"github.com/mholt/archiver/v4"
	"golang.org/x/sync/errgroup"

	"github.com/pterodactyl/wings/config"
	"github.com/pterodactyl/wings/remote"
)

var format = archiver.CompressedArchive{
	Compression: archiver.Gz{},
	Archival:    archiver.Tar{},
}

type AdapterType string

const (
	LocalBackupAdapter AdapterType = "wings"
	S3BackupAdapter    AdapterType = "s3"
)

// RestoreCallback is a generic restoration callback that exists for both local
// and remote backups allowing the files to be restored.
type RestoreCallback func(file string, info fs.FileInfo, r io.ReadCloser) error

// noinspection GoNameStartsWithPackageName
type BackupInterface interface {
	// SetClient sets the API request client on the backup interface.
	SetClient(remote.Client)
	// Identifier returns the UUID of this backup as tracked by the panel
	// instance.
	Identifier() string
	// WithLogContext attaches additional context to the log output for this
	// backup.
	WithLogContext(map[string]interface{})
	// Generate creates a backup in whatever the configured source for the
	// specific implementation is.
	Generate(context.Context, string, string) (*ArchiveDetails, error)
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
	Details(context.Context, []remote.BackupPart) (*ArchiveDetails, error)
	// Remove removes a backup file.
	Remove() error
	// Restore is called when a backup is ready to be restored to the disk from
	// the given source. Not every backup implementation will support this nor
	// will every implementation require a reader be provided.
	Restore(context.Context, io.Reader, RestoreCallback) error
}

type Backup struct {
	// The UUID of this backup object. This must line up with a backup from
	// the panel instance.
	Uuid string `json:"uuid"`

	// An array of files to ignore when generating this backup. This should be
	// compatible with a standard .gitignore structure.
	Ignore string `json:"ignore"`

	client     remote.Client
	adapter    AdapterType
	logContext map[string]interface{}
}

func (b *Backup) SetClient(c remote.Client) {
	b.client = c
}

func (b *Backup) Identifier() string {
	return b.Uuid
}

// Path returns the path for this specific backup.
func (b *Backup) Path() string {
	return path.Join(config.Get().System.BackupDirectory, b.Identifier()+".tar.gz")
}

// Size returns the size of the generated backup.
func (b *Backup) Size() (int64, error) {
	st, err := os.Stat(b.Path())
	if err != nil {
		return 0, err
	}

	return st.Size(), nil
}

// Checksum returns the SHA256 checksum of a backup.
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

// Details returns both the checksum and size of the archive currently stored on
// the disk to the caller.
func (b *Backup) Details(ctx context.Context, parts []remote.BackupPart) (*ArchiveDetails, error) {
	ad := ArchiveDetails{ChecksumType: "sha1", Parts: parts}
	g, ctx := errgroup.WithContext(ctx)

	g.Go(func() error {
		resp, err := b.Checksum()
		if err != nil {
			return err
		}
		ad.Checksum = hex.EncodeToString(resp)
		return nil
	})

	g.Go(func() error {
		s, err := b.Size()
		if err != nil {
			return err
		}
		ad.Size = s
		return nil
	})

	if err := g.Wait(); err != nil {
		return nil, errors.WithStackDepth(err, 1)
	}
	return &ad, nil
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

type ArchiveDetails struct {
	Checksum     string              `json:"checksum"`
	ChecksumType string              `json:"checksum_type"`
	Size         int64               `json:"size"`
	Parts        []remote.BackupPart `json:"parts"`
}

// ToRequest returns a request object.
func (ad *ArchiveDetails) ToRequest(successful bool) remote.BackupRequest {
	return remote.BackupRequest{
		Checksum:     ad.Checksum,
		ChecksumType: ad.ChecksumType,
		Size:         ad.Size,
		Successful:   successful,
		Parts:        ad.Parts,
	}
}
