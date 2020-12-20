package filesystem

import (
	"emperror.dev/errors"
	"fmt"
	"github.com/apex/log"
	"os"
	"path/filepath"
)

type ErrorCode string

const (
	ErrCodeIsDirectory    ErrorCode = "E_ISDIR"
	ErrCodeDiskSpace      ErrorCode = "E_NODISK"
	ErrCodeUnknownArchive ErrorCode = "E_UNKNFMT"
	ErrCodePathResolution ErrorCode = "E_BADPATH"
)

type Error struct {
	code     ErrorCode
	path     string
	resolved string
}

// Returns a human-readable error string to identify the Error by.
func (e *Error) Error() string {
	switch e.code {
	case ErrCodeIsDirectory:
		return "filesystem: is a directory"
	case ErrCodeDiskSpace:
		return "filesystem: not enough disk space"
	case ErrCodeUnknownArchive:
		return "filesystem: unknown archive format"
	case ErrCodePathResolution:
		r := e.resolved
		if r == "" {
			r = "<empty>"
		}
		return fmt.Sprintf("filesystem: server path [%s] resolves to a location outside the server root: %s", e.path, r)
	}
	return "filesystem: unhandled error type"
}

// Returns the ErrorCode for this specific error instance.
func (e *Error) Code() ErrorCode {
	return e.code
}

// Checks if the given error is one of the Filesystem errors.
func IsFilesystemError(err error) (*Error, bool) {
	if e := errors.Unwrap(err); e != nil {
		err = e
	}
	if fserr, ok := err.(*Error); ok {
		return fserr, true
	}
	return nil, false
}

// Checks if "err" is a filesystem Error type. If so, it will then drop in and check
// that the error code is the same as the provided ErrorCode passed in "code".
func IsErrorCode(err error, code ErrorCode) bool {
	if e, ok := IsFilesystemError(err); ok {
		return e.code == code
	}
	return false
}

// Returns a new BadPathResolution error.
func NewBadPathResolution(path string, resolved string) *Error {
	return &Error{code: ErrCodePathResolution, path: path, resolved: resolved}
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
	if !IsErrorCode(err, ErrCodePathResolution) {
		return err
	}

	if f != nil && f.IsDir() {
		return filepath.SkipDir
	}

	return nil
}
