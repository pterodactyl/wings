// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: Copyright (c) 2024 Matthew Penner

package ufs

import (
	"errors"
	iofs "io/fs"
	"os"

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

// NewSyscallError returns, as an error, a new SyscallError
// with the given system call name and error details.
// As a convenience, if err is nil, NewSyscallError returns nil.
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
	switch {
	case errors.As(err, &pErr):
		switch {
		// File exists
		case errors.Is(pErr.Err, unix.EEXIST):
			return &PathError{
				Op:   pErr.Op,
				Path: pErr.Path,
				Err:  ErrExist,
			}
		// Is a directory
		case errors.Is(pErr.Err, unix.EISDIR):
			return &PathError{
				Op:   pErr.Op,
				Path: pErr.Path,
				Err:  ErrIsDirectory,
			}
		// Not a directory
		case errors.Is(pErr.Err, unix.ENOTDIR):
			return &PathError{
				Op:   pErr.Op,
				Path: pErr.Path,
				Err:  ErrNotDirectory,
			}
		// No such file or directory
		case errors.Is(pErr.Err, unix.ENOENT):
			return &PathError{
				Op:   pErr.Op,
				Path: pErr.Path,
				Err:  ErrNotExist,
			}
		// Operation not permitted
		case errors.Is(pErr.Err, unix.EPERM):
			return &PathError{
				Op:   pErr.Op,
				Path: pErr.Path,
				Err:  ErrPermission,
			}
		// Invalid cross-device link
		case errors.Is(pErr.Err, unix.EXDEV):
			return &PathError{
				Op:   pErr.Op,
				Path: pErr.Path,
				Err:  ErrBadPathResolution,
			}
		// Too many levels of symbolic links
		case errors.Is(pErr.Err, unix.ELOOP):
			return &PathError{
				Op:   pErr.Op,
				Path: pErr.Path,
				Err:  ErrBadPathResolution,
			}
		}
	}
	return err
}
