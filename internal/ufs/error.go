// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: Copyright (c) 2024 Matthew Penner

package ufs

import (
	"errors"
	iofs "io/fs"
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

var (
	// ErrIsDirectory is an error for when an operation that operates only on
	// files is given a path to a directory.
	ErrIsDirectory = errors.New("is a directory")
	// ErrNotDirectory is an error for when an operation that operates only on
	// directories is given a path to a file.
	ErrNotDirectory = errors.New("not a directory")
	// ErrBadPathResolution is an error for when a sand-boxed filesystem
	// resolves a given path to a forbidden location.
	ErrBadPathResolution = errors.New("bad path resolution")
	// ErrNotRegular is an error for when an operation that operates only on
	// regular files is passed something other than a regular file.
	ErrNotRegular = errors.New("not a regular file")

	// ErrClosed is an error for when an entry was accessed after being closed.
	ErrClosed = iofs.ErrClosed
	// ErrInvalid is an error for when an invalid argument was used.
	ErrInvalid = iofs.ErrInvalid
	// ErrExist is an error for when an entry already exists.
	ErrExist = iofs.ErrExist
	// ErrNotExist is an error for when an entry does not exist.
	ErrNotExist = iofs.ErrNotExist
	// ErrPermission is an error for when the required permissions to perform an
	// operation are missing.
	ErrPermission = iofs.ErrPermission
)

// LinkError records an error during a link or symlink or rename
// system call and the paths that caused it.
type LinkError = os.LinkError

// PathError records an error and the operation and file path that caused it.
type PathError = iofs.PathError

// SyscallError records an error from a specific system call.
type SyscallError = os.SyscallError

// NewSyscallError returns, as an error, a new [*os.SyscallError] with the
// given system call name and error details. As a convenience, if err is nil,
// [NewSyscallError] returns nil.
func NewSyscallError(syscall string, err error) error {
	return os.NewSyscallError(syscall, err)
}

// convertErrorType converts errors into our custom errors to ensure consistent
// error values.
func convertErrorType(err error) error {
	if err == nil {
		return nil
	}

	var pErr *PathError
	if errors.As(err, &pErr) {
		if errno, ok := pErr.Err.(syscall.Errno); ok {
			return errnoToPathError(errno, pErr.Op, pErr.Path)
		}
		return pErr
	}

	// If the error wasn't already a path error and is a errno, wrap it with
	// details that we can use to know there is something wrong with our
	// error wrapping somewhere.
	var errno syscall.Errno
	if errors.As(err, &errno) {
		return &PathError{
			Op:   "!(UNKNOWN)",
			Path: "!(UNKNOWN)",
			Err:  err,
		}
	}

	return err
}

// ensurePathError ensures that err is a PathError. The op and path arguments
// are only used of the error isn't already a PathError.
func ensurePathError(err error, op, path string) error {
	if err == nil {
		return nil
	}

	// Check if the error is already a PathError.
	var pErr *PathError
	if errors.As(err, &pErr) {
		// If underlying error is a errno, convert it.
		//
		// DO NOT USE `errors.As` or whatever here, the error will either be
		// an errno, or it will be wrapped already.
		if errno, ok := pErr.Err.(syscall.Errno); ok {
			return errnoToPathError(errno, pErr.Op, pErr.Path)
		}
		// Return the PathError as-is without modification.
		return pErr
	}

	// If the error is directly an errno, convert it to a PathError.
	var errno syscall.Errno
	if errors.As(err, &errno) {
		return errnoToPathError(errno, op, path)
	}

	// Otherwise just wrap it as a PathError without any additional changes.
	return &PathError{
		Op:   op,
		Path: path,
		Err:  err,
	}
}

// errnoToPathError converts an errno into a proper path error.
func errnoToPathError(err syscall.Errno, op, path string) error {
	switch err {
	// File exists
	case unix.EEXIST:
		return &PathError{
			Op:   op,
			Path: path,
			Err:  ErrExist,
		}
	// Is a directory
	case unix.EISDIR:
		return &PathError{
			Op:   op,
			Path: path,
			Err:  ErrIsDirectory,
		}
	// Not a directory
	case unix.ENOTDIR:
		return &PathError{
			Op:   op,
			Path: path,
			Err:  ErrNotDirectory,
		}
	// No such file or directory
	case unix.ENOENT:
		return &PathError{
			Op:   op,
			Path: path,
			Err:  ErrNotExist,
		}
	// Operation not permitted
	case unix.EPERM:
		return &PathError{
			Op:   op,
			Path: path,
			Err:  ErrPermission,
		}
	// Invalid cross-device link
	case unix.EXDEV:
		return &PathError{
			Op:   op,
			Path: path,
			Err:  ErrBadPathResolution,
		}
	// Too many levels of symbolic links
	case unix.ELOOP:
		return &PathError{
			Op:   op,
			Path: path,
			Err:  ErrBadPathResolution,
		}
	default:
		return &PathError{
			Op:   op,
			Path: path,
			Err:  err,
		}
	}
}
