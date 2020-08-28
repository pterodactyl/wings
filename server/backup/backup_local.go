package backup

import (
	"context"
	"github.com/pkg/errors"
	"os"
)

type LocalBackup struct {
	Backup
}

var _ BackupInterface = (*LocalBackup)(nil)

// Locates the backup for a server and returns the local path. This will obviously only
// work if the backup was created as a local backup.
func LocateLocal(uuid string) (*LocalBackup, os.FileInfo, error) {
	b := &LocalBackup{
		Backup{
			Uuid:         uuid,
			IgnoredFiles: nil,
		},
	}

	st, err := os.Stat(b.Path())
	if err != nil {
		return nil, nil, errors.WithStack(err)
	}

	if st.IsDir() {
		return nil, nil, errors.New("invalid archive found; is directory")
	}

	return b, st, nil
}

// Removes a backup from the system.
func (b *LocalBackup) Remove() error {
	return os.Remove(b.Path())
}

// Generates a backup of the selected files and pushes it to the defined location
// for this instance.
func (b *LocalBackup) Generate(included *IncludedFiles, prefix string) (*ArchiveDetails, error) {
	a := &Archive{
		TrimPrefix: prefix,
		Files:      included,
	}

	if _, err := a.Create(b.Path(), context.Background()); err != nil {
		return nil, errors.WithStack(err)
	}

	return b.Details(), nil
}
