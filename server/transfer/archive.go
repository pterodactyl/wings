package transfer

import (
	"context"
	"fmt"
	"io"

	"github.com/pterodactyl/wings/internal/progress"
	"github.com/pterodactyl/wings/server/filesystem"
)

// Archive .
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

// Archive .
type Archive struct {
	archive *filesystem.Archive
}

// NewArchive .
func NewArchive(t *Transfer, size uint64) *Archive {
	return &Archive{
		archive: &filesystem.Archive{
			BasePath: t.Server.Filesystem().Path(),
			Progress: progress.NewProgress(size),
		},
	}
}

// Stream .
func (a *Archive) Stream(ctx context.Context, w io.Writer) error {
	return a.archive.Stream(ctx, w)
}

// Progress .
func (a *Archive) Progress() *progress.Progress {
	return a.archive.Progress
}
