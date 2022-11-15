package backup

import (
	"context"
	"io"
	"os"

	"emperror.dev/errors"
	"github.com/juju/ratelimit"
	"github.com/mholt/archiver/v4"

	"github.com/pterodactyl/wings/config"
	"github.com/pterodactyl/wings/remote"
	"github.com/pterodactyl/wings/server/filesystem"
)

type LocalBackup struct {
	Backup
}

var _ BackupInterface = (*LocalBackup)(nil)

func NewLocal(client remote.Client, uuid string, ignore string) *LocalBackup {
	return &LocalBackup{
		Backup{
			client:  client,
			Uuid:    uuid,
			Ignore:  ignore,
			adapter: LocalBackupAdapter,
		},
	}
}

// LocateLocal finds the backup for a server and returns the local path. This
// will obviously only work if the backup was created as a local backup.
func LocateLocal(client remote.Client, uuid string) (*LocalBackup, os.FileInfo, error) {
	b := NewLocal(client, uuid, "")
	st, err := os.Stat(b.Path())
	if err != nil {
		return nil, nil, err
	}

	if st.IsDir() {
		return nil, nil, errors.New("invalid archive, is directory")
	}

	return b, st, nil
}

// Remove removes a backup from the system.
func (b *LocalBackup) Remove() error {
	return os.Remove(b.Path())
}

// WithLogContext attaches additional context to the log output for this backup.
func (b *LocalBackup) WithLogContext(c map[string]interface{}) {
	b.logContext = c
}

// Generate generates a backup of the selected files and pushes it to the
// defined location for this instance.
func (b *LocalBackup) Generate(ctx context.Context, basePath, ignore string) (*ArchiveDetails, error) {
	a := &filesystem.Archive{
		BasePath: basePath,
		Ignore:   ignore,
	}

	b.log().WithField("path", b.Path()).Info("creating backup for server")
	if err := a.Create(ctx, b.Path()); err != nil {
		return nil, err
	}
	b.log().Info("created backup successfully")

	ad, err := b.Details(ctx, nil)
	if err != nil {
		return nil, errors.WrapIf(err, "backup: failed to get archive details for local backup")
	}
	return ad, nil
}

// Restore will walk over the archive and call the callback function for each
// file encountered.
func (b *LocalBackup) Restore(ctx context.Context, _ io.Reader, callback RestoreCallback) error {
	f, err := os.Open(b.Path())
	if err != nil {
		return err
	}

	var reader io.Reader = f
	// Steal the logic we use for making backups which will be applied when restoring
	// this specific backup. This allows us to prevent overloading the disk unintentionally.
	if writeLimit := int64(config.Get().System.Backups.WriteLimit * 1024 * 1024); writeLimit > 0 {
		reader = ratelimit.Reader(f, ratelimit.NewBucketWithRate(float64(writeLimit), writeLimit))
	}
	if err := format.Extract(ctx, reader, nil, func(ctx context.Context, f archiver.File) error {
		r, err := f.Open()
		if err != nil {
			return err
		}
		defer r.Close()

		return callback(filesystem.ExtractNameFromArchive(f), f.FileInfo, r)
	}); err != nil {
		return err
	}
	return nil
}
