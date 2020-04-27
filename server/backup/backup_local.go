package backup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"github.com/pkg/errors"
	"github.com/pterodactyl/wings/config"
	"go.uber.org/zap"
	"io"
	"os"
	"path"
	"sync"
)

type LocalBackup struct {
	// The UUID of this backup object. This must line up with a backup from
	// the panel instance.
	Uuid string `json:"uuid"`

	// An array of files to ignore when generating this backup. This should be
	// compatible with a standard .gitignore structure.
	IgnoredFiles []string `json:"ignored_files"`
}

var _ Backup = (*LocalBackup)(nil)

// Locates the backup for a server and returns the local path. This will obviously only
// work if the backup was created as a local backup.
func LocateLocal(uuid string) (*LocalBackup, os.FileInfo, error) {
	b := &LocalBackup{
		Uuid:         uuid,
		IgnoredFiles: nil,
	}

	st, err := os.Stat(b.Path())
	if err != nil {
		return nil, nil, err
	}

	if st.IsDir() {
		return nil, nil, errors.New("invalid archive found; is directory")
	}

	return b, st, nil
}

func (b *LocalBackup) Identifier() string {
	return b.Uuid
}

// Returns the path for this specific backup.
func (b *LocalBackup) Path() string {
	return path.Join(config.Get().System.BackupDirectory, b.Uuid+".tar.gz")
}

// Returns the SHA256 checksum of a backup.
func (b *LocalBackup) Checksum() ([]byte, error) {
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
func (b *LocalBackup) Remove() error {
	return os.Remove(b.Path())
}

// Generates a backup of the selected files and pushes it to the defined location
// for this instance.
func (b *LocalBackup) Backup(included *IncludedFiles, prefix string) error {
	a := &Archive{
		TrimPrefix: prefix,
		Files:      included,
	}

	err := a.Create(b.Path(), context.Background())

	return err
}

// Return the size of the generated backup.
func (b *LocalBackup) Size() (int64, error) {
	st, err := os.Stat(b.Path())
	if err != nil {
		return 0, errors.WithStack(err)
	}

	return st.Size(), nil
}

// Returns details of the archive by utilizing two go-routines to get the checksum and
// the size of the archive.
func (b *LocalBackup) Details() *ArchiveDetails {
	wg := sync.WaitGroup{}
	wg.Add(2)

	var checksum string
	// Calculate the checksum for the file.
	go func() {
		defer wg.Done()

		resp, err := b.Checksum()
		if err != nil {
			zap.S().Errorw("failed to calculate checksum for backup", zap.String("backup", b.Uuid), zap.Error(err))
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

func (b *LocalBackup) Ignored() []string {
	return b.IgnoredFiles
}