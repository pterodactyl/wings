package backup

import (
	"encoding/hex"
	"github.com/mholt/archiver/v3"
	"github.com/pkg/errors"
	"go.uber.org/zap"
	"os"
	"strings"
	"sync"
)

// Locates the backup for a server and returns the local path. This will obviously only
// work if the backup was created as a local backup.
func LocateLocal(uuid string) (*Backup, os.FileInfo, error) {
	b := &Backup{
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

// Generates a backup of the selected files and pushes it to the defined location
// for this instance.
func (b *Backup) LocalBackup(dir string) (*ArchiveDetails, error) {
	if err := archiver.Archive([]string{dir}, b.Path()); err != nil {
		if strings.HasPrefix(err.Error(), "file already exists") {
			if rerr := os.Remove(b.Path()); rerr != nil {
				return nil, errors.WithStack(rerr)
			}

			// Re-attempt this backup by calling it with the same information.
			return b.LocalBackup(dir)
		}

		// If there was some error with the archive, just go ahead and ensure the backup
		// is completely destroyed at this point. Ignore any errors from this function.
		os.Remove(b.Path())

		return nil, err
	}

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

		st, err := os.Stat(b.Path())
		if err != nil {
			return
		}

		sz = st.Size()
	}()

	wg.Wait()

	return &ArchiveDetails{
		Checksum: checksum,
		Size:     sz,
	}, nil
}
