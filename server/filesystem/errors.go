package filesystem

import (
	"fmt"
	"os"

	"emperror.dev/errors"
	"github.com/apex/log"
)

type ErrorCode string

const (
	ErrCodeIsDirectory    ErrorCode = "E_ISDIR"
	ErrCodeDiskSpace      ErrorCode = "E_NODISK"
	ErrCodeUnknownArchive ErrorCode = "E_UNKNFMT"
	ErrCodeDenylistFile   ErrorCode = "E_DENYLIST"
	ErrCodeUnknownError   ErrorCode = "E_UNKNOWN"
	ErrNotExist           ErrorCode = "E_NOTEXIST"
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
	case ErrNotExist:
		return "filesystem: does not exist"
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

func IsPathError(err error) bool {
	var pe *os.PathError
	return errors.As(err, &pe)
}

func IsLinkError(err error) bool {
	var le *os.LinkError
	return errors.As(err, &le)
}
