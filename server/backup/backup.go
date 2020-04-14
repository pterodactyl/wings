package backup

import (
	"crypto/sha256"
	"github.com/pkg/errors"
	"github.com/pterodactyl/wings/api"
	"github.com/pterodactyl/wings/config"
	"go.uber.org/zap"
	"io"
	"os"
	"path"
)

type Backup struct {
	// The UUID of this backup object. This must line up with a backup from
	// the panel instance.
	Uuid string `json:"uuid"`

	// An array of files to ignore when generating this backup. This should be
	// compatible with a standard .gitignore structure.
	IgnoredFiles []string `json:"ignored_files"`
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

// Returns the path for this specific backup.
func (b *Backup) Path() string {
	return path.Join(config.Get().System.BackupDirectory, b.Uuid+".tar.gz")
}

// Returns the SHA256 checksum of a backup.
func (b *Backup) Checksum() ([]byte, error) {
	h := sha256.New()

	f, err := os.Open(b.Path())
	if err != nil {
		return []byte{}, errors.WithStack(err)
	}
	defer f.Close()

	if _, err := io.Copy(h, f); err != nil {
		return []byte{}, errors.WithStack(err)
	}

	return h.Sum(nil), nil
}

// Removes a backup from the system.
func (b *Backup) Remove() error {
	return os.Remove(b.Path())
}

// Notifies the panel of a backup's state and returns an error if one is encountered
// while performing this action.
func (b *Backup) NotifyPanel(ad *ArchiveDetails, successful bool) error {
	r := api.NewRequester()

	rerr, err := r.SendBackupStatus(b.Uuid, ad.ToRequest(successful))
	if rerr != nil || err != nil {
		if err != nil {
			zap.S().Errorw(
				"failed to notify panel of backup status due to internal code error",
				zap.String("backup", b.Uuid),
				zap.Error(err),
			)

			return err
		}

		zap.S().Warnw(rerr.String(), zap.String("backup", b.Uuid))

		return errors.New(rerr.String())
	}

	return nil
}

// Ensures that the local backup destination for files exists.
func (b *Backup) ensureLocalBackupLocation() error {
	d := config.Get().System.BackupDirectory

	if _, err := os.Stat(d); err != nil {
		if !os.IsNotExist(err) {
			return errors.WithStack(err)
		}

		return os.MkdirAll(d, 0700)
	}

	return nil
}
