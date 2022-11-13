package transfer

import (
	"context"
	"fmt"
	"io"

	"github.com/pterodactyl/wings/internal/progress"
	"github.com/pterodactyl/wings/server/filesystem"
)

// Archive returns an archive that can be used to stream the contents of the
// contents of a server.
func (t *Transfer) Archive() (*Archive, error) {
	if t.archive == nil {
		// Get the disk usage of the server (used to calculate the progress of the archive process)
		rawSize, err := t.Server.Filesystem().DiskUsage(true)
		if err != nil {
			return nil, fmt.Errorf("transfer: failed to get server disk usage: %w", err)
		}

		// Create a new archive instance and assign it to the transfer.
		t.archive = NewArchive(t, uint64(rawSize))
	}

	return t.archive, nil
}

// Archive represents an archive used to transfer the contents of a server.
type Archive struct {
	archive *filesystem.Archive
}

// NewArchive returns a new archive associated with the given transfer.
func NewArchive(t *Transfer, size uint64) *Archive {
	return &Archive{
		archive: &filesystem.Archive{
			BasePath: t.Server.Filesystem().Path(),
			Progress: progress.NewProgress(size),
		},
	}
}

// Stream returns a reader that can be used to stream the contents of the archive.
func (a *Archive) Stream(ctx context.Context, w io.Writer) error {
	return a.archive.Stream(ctx, w)
}

// Progress returns the current progress of the archive.
func (a *Archive) Progress() *progress.Progress {
	return a.archive.Progress
}
