package transfer

import (
	"context"
	"fmt"
	"io"
	"os"

	"emperror.dev/errors"
	"github.com/pterodactyl/wings/internal/progress"
	"github.com/pterodactyl/wings/server/filesystem"
)

// Archive returns an archive that can be used to stream the contents of the
// contents of a server.
func (t *Transfer) Archive(r *os.Root) (*Archive, error) {
	if t.archive == nil {
		// Get the disk usage of the server (used to calculate the progress of the archive process)
		rawSize, err := t.Server.Filesystem().DiskUsage(true)
		if err != nil {
			return nil, fmt.Errorf("transfer: failed to get server disk usage: %w", err)
		}

		a, err := filesystem.NewArchive(r, "/", filesystem.WithProgress(progress.NewProgress(uint64(rawSize))))
		if err != nil {
			_ = r.Close()
			return nil, errors.WrapIf(err, "server/transfer: failed to create archive")
		}
		t.archive = &Archive{archive: a}
	}

	return t.archive, nil
}

// Archive represents an archive used to transfer the contents of a server.
type Archive struct {
	archive *filesystem.Archive
}

// Stream returns a reader that can be used to stream the contents of the archive.
func (a *Archive) Stream(ctx context.Context, w io.Writer) error {
	return a.archive.Stream(ctx, w)
}

// Progress returns the current progress of the archive.
func (a *Archive) Progress() *progress.Progress {
	return a.archive.Progress()
}
