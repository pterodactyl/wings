package filesystem

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"emperror.dev/errors"
	"github.com/apex/log"
)

type ErrorCode string

const (
	ErrCodeIsDirectory    ErrorCode = "E_ISDIR"
	ErrCodeDiskSpace      ErrorCode = "E_NODISK"
	ErrCodeUnknownArchive ErrorCode = "E_UNKNFMT"
	ErrCodePathResolution ErrorCode = "E_BADPATH"
	ErrCodeDenylistFile   ErrorCode = "E_DENYLIST"
	ErrCodeUnknownError   ErrorCode = "E_UNKNOWN"
)

type Error struct {
	code ErrorCode
	// Contains the underlying error leading to this. This value may or may not be
	// present, it is entirely dependent on how this error was triggered.
	err error
	// This contains the value of the final destination that triggered this specific
	// error event.
	resolved string
	// This value is generally only present on errors stemming from a path resolution
	// error. For everything else you should be setting and reading the resolved path
	// value which will be far more useful.
	path string
}

// newFilesystemError returns a new error instance with a stack trace associated.
func newFilesystemError(code ErrorCode, err error) error {
	if err != nil {
		return errors.WithStackDepth(&Error{code: code, err: err}, 1)
	}
	return errors.WithStackDepth(&Error{code: code}, 1)
}

// Code returns the ErrorCode for this specific error instance.
func (e *Error) Code() ErrorCode {
	return e.code
}

// Returns a human-readable error string to identify the Error by.
func (e *Error) Error() string {
	switch e.code {
	case ErrCodeIsDirectory:
		return fmt.Sprintf("filesystem: cannot perform action: [%s] is a directory", e.resolved)
	case ErrCodeDiskSpace:
		return "filesystem: not enough disk space"
	case ErrCodeUnknownArchive:
		return "filesystem: unknown archive format"
	case ErrCodeDenylistFile:
		r := e.resolved
		if r == "" {
			r = "<empty>"
		}
		return fmt.Sprintf("filesystem: file access prohibited: [%s] is on the denylist", r)
	case ErrCodePathResolution:
		r := e.resolved
		if r == "" {
			r = "<empty>"
		}
		return fmt.Sprintf("filesystem: server path [%s] resolves to a location outside the server root: %s", e.path, r)
	case ErrCodeUnknownError:
		fallthrough
	default:
		return fmt.Sprintf("filesystem: an error occurred: %s", e.Unwrap())
	}
}

// Unwrap returns the underlying cause of this filesystem error. In some causes
// there may not be a cause present, in which case nil will be returned.
func (e *Error) Unwrap() error {
	return e.err
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

// IsFilesystemError checks if the given error is one of the Filesystem errors.
func IsFilesystemError(err error) bool {
	var fserr *Error
	if err != nil && errors.As(err, &fserr) {
		return true
	}
	return false
}

// IsErrorCode checks if "err" is a filesystem Error type. If so, it will then
// drop in and check that the error code is the same as the provided ErrorCode
// passed in "code".
func IsErrorCode(err error, code ErrorCode) bool {
	var fserr *Error
	if err != nil && errors.As(err, &fserr) {
		return fserr.code == code
	}
	return false
}

// IsUnknownArchiveFormatError checks if the error is due to the archive being
// in an unexpected file format.
func IsUnknownArchiveFormatError(err error) bool {
	if err != nil && strings.HasPrefix(err.Error(), "format ") {
		return true
	}
	return false
}

// NewBadPathResolution returns a new BadPathResolution error.
func NewBadPathResolution(path string, resolved string) error {
	return errors.WithStackDepth(&Error{code: ErrCodePathResolution, path: path, resolved: resolved}, 1)
}

// wrapError wraps the provided error as a Filesystem error and attaches the
// provided resolved source to it. If the error is already a Filesystem error
// no action is taken.
func wrapError(err error, resolved string) error {
	if err == nil || IsFilesystemError(err) {
		return err
	}
	return errors.WithStackDepth(&Error{code: ErrCodeUnknownError, err: err, resolved: resolved}, 1)
}
