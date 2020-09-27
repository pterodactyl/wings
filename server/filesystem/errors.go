package filesystem

import (
	"github.com/apex/log"
	"github.com/pkg/errors"
	"os"
	"path/filepath"
)

var ErrIsDirectory = errors.New("filesystem: is a directory")
var ErrNotEnoughDiskSpace = errors.New("filesystem: not enough disk space")
var ErrBadPathResolution = errors.New("filesystem: invalid path resolution")
var ErrUnknownArchiveFormat = errors.New("filesystem: unknown archive format")

// Generates an error logger instance with some basic information.
func (fs *Filesystem) error(err error) *log.Entry {
	return log.WithField("subsystem", "filesystem").WithField("root", fs.root).WithField("error", err)
}

// Handle errors encountered when walking through directories.
//
// If there is a path resolution error just skip the item entirely. Only return this for a
// directory, otherwise return nil. Returning this error for a file will stop the walking
// for the remainder of the directory. This is assuming an os.FileInfo struct was even returned.
func (fs *Filesystem) handleWalkerError(err error, f os.FileInfo) error {
	if !errors.Is(err, ErrBadPathResolution) {
		return err
	}

	if f != nil && f.IsDir() {
		return filepath.SkipDir
	}

	return nil
}