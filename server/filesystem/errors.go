package filesystem

import (
	"emperror.dev/errors"
	"fmt"
	"github.com/apex/log"
	"os"
	"path/filepath"
)

var ErrIsDirectory = errors.Sentinel("filesystem: is a directory")
var ErrNotEnoughDiskSpace = errors.Sentinel("filesystem: not enough disk space")
var ErrUnknownArchiveFormat = errors.Sentinel("filesystem: unknown archive format")

type BadPathResolutionError struct {
	path     string
	resolved string
}

// Returns the specific error for a bad path resolution.
func (b *BadPathResolutionError) Error() string {
	r := b.resolved
	if r == "" {
		r = "<empty>"
	}
	return fmt.Sprintf("filesystem: server path [%s] resolves to a location outside the server root: %s", b.path, r)
}

// Returns a new BadPathResolution error.
func NewBadPathResolution(path string, resolved string) *BadPathResolutionError {
	return &BadPathResolutionError{path, resolved}
}

// Determines if the given error is a bad path resolution error.
func IsBadPathResolutionError(err error) bool {
	e := errors.Unwrap(err)
	if e == nil {
		e = err
	}

	if _, ok := e.(*BadPathResolutionError); ok {
		return true
	}

	return false
}

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
	if !IsBadPathResolutionError(err) {
		return errors.WithStackIf(err)
	}

	if f != nil && f.IsDir() {
		return filepath.SkipDir
	}

	return nil
}
