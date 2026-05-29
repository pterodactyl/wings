// SPDX-License-Identifier: BSD-3-Clause

// Code in this file was derived from `go/src/os/removeall_at.go`.

// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the `go.LICENSE` file.

//go:build unix

package ufs

import (
	"errors"
	"io"
	"os"

	"golang.org/x/sys/unix"
)

type unixFS interface {
	Open(name string) (File, error)
	Remove(name string) error
	unlinkat(dirfd int, path string, flags int) error
}

func (fs *UnixFS) removeAll(path string) error {
	return removeAll(fs, path)
}

func removeAll(fs unixFS, path string) error {
	if path == "" {
		// fail silently to retain compatibility with previous behavior
		// of RemoveAll. See issue https://go.dev/issue/28830.
		return nil
	}

	// The rmdir system call does not permit removing ".",
	// so we don't permit it either.
	if endsWithDot(path) {
		return &PathError{Op: "removeall", Path: path, Err: unix.EINVAL}
	}

	// Simple case: if Remove works, we're done.
	err := fs.Remove(path)
	if err == nil || errors.Is(err, ErrNotExist) {
		return nil
	}

	// RemoveAll recurses by deleting the path base from
	// its parent directory
	parentDir, base := splitPath(path)

	parent, err := fs.Open(parentDir)
	if err != nil {
		if !errors.Is(err, ErrNotExist) {
			return err
		}
		// If parent does not exist, base cannot exist. Fail silently
		return nil
	}
	defer parent.Close()

	if err := removeAllFrom(fs, parent, base); err != nil {
		if pathErr, ok := err.(*PathError); ok {
			pathErr.Path = parentDir + string(os.PathSeparator) + pathErr.Path
			err = convertErrorType(pathErr)
		} else {
			err = ensurePathError(err, "removeallfrom", base)
		}
		return err
	}
	return nil
}

func (fs *UnixFS) removeContents(path string) error {
	return removeContents(fs, path)
}

func removeContents(fs unixFS, path string) error {
	if path == "" {
		// fail silently to retain compatibility with previous behavior
		// of RemoveAll. See issue https://go.dev/issue/28830.
		return nil
	}

	// RemoveAll recurses by deleting the path base from
	// its parent directory
	parentDir, base := splitPath(path)

	parent, err := fs.Open(parentDir)
	if err != nil {
		if !errors.Is(err, ErrNotExist) {
			return err
		}
		// If parent does not exist, base cannot exist. Fail silently
		return nil
	}
	defer parent.Close()

	if err := removeContentsFrom(fs, parent, base); err != nil {
		if pathErr, ok := err.(*PathError); ok {
			pathErr.Path = parentDir + string(os.PathSeparator) + pathErr.Path
			err = convertErrorType(pathErr)
		} else {
			err = ensurePathError(err, "removecontentsfrom", base)
		}
		return err
	}
	return nil
}

// removeContentsFrom recursively removes all descendants of parent without
// removing parent itself. Parent must be a directory.
func removeContentsFrom(fs unixFS, parent File, base string) error {
	parentFd := int(parent.Fd())

	var recurseErr error
	for {
		const reqSize = 1024
		var respSize int

		// Open the directory to recurse into
		file, err := openFdAt(parentFd, base)
		if err != nil {
			if errors.Is(err, ErrNotExist) {
				return nil
			}
			recurseErr = &PathError{Op: "openfdat", Path: base, Err: err}
			break
		}

		for {
			numErr := 0

			names, readErr := file.Readdirnames(reqSize)
			// Errors other than EOF should stop us from continuing.
			if readErr != nil && readErr != io.EOF {
				_ = file.Close()
				if errors.Is(readErr, ErrNotExist) {
					return nil
				}
				return &PathError{Op: "readdirnames", Path: base, Err: readErr}
			}

			respSize = len(names)
			for _, name := range names {
				err := removeAllFrom(fs, file, name)
				if err != nil {
					if pathErr, ok := err.(*PathError); ok {
						pathErr.Path = base + string(os.PathSeparator) + pathErr.Path
					}
					numErr++
					if recurseErr == nil {
						recurseErr = err
					}
				}
			}

			// If we can delete any entry, break to start new iteration.
			// Otherwise, we discard current names, get next entries and try deleting them.
			if numErr != reqSize {
				break
			}
		}

		// Removing files from the directory may have caused
		// the OS to reshuffle it. Simply calling Readdirnames
		// again may skip some entries. The only reliable way
		// to avoid this is to close and re-open the
		// directory. See issue https://go.dev/issue/20841.
		_ = file.Close()

		// Finish when the end of the directory is reached
		if respSize < reqSize {
			break
		}
	}

	return nil
}

func removeAllFrom(fs unixFS, parent File, base string) error {
	parentFd := int(parent.Fd())

	// Simple case: if Unlink (aka remove) works, we're done.
	err := fs.unlinkat(parentFd, base, 0)
	if err == nil || errors.Is(err, ErrNotExist) {
		return nil
	}

	// EISDIR means that we have a directory, and we need to
	// remove its contents.
	// EPERM or EACCES means that we don't have write permission on
	// the parent directory, but this entry might still be a directory
	// whose contents need to be removed.
	// Otherwise, just return the error.
	if err != unix.EISDIR && err != unix.EPERM && err != unix.EACCES {
		return &PathError{Op: "unlinkat", Path: base, Err: err}
	}

	// Is this a directory we need to recurse into?
	var statInfo unix.Stat_t
	statErr := ignoringEINTR(func() error {
		return unix.Fstatat(parentFd, base, &statInfo, AT_SYMLINK_NOFOLLOW)
	})
	if statErr != nil {
		if errors.Is(statErr, ErrNotExist) {
			return nil
		}
		return &PathError{Op: "fstatat", Path: base, Err: statErr}
	}
	if statInfo.Mode&unix.S_IFMT != unix.S_IFDIR {
		// Not a directory; return the error from the unix.Unlinkat.
		return &PathError{Op: "unlinkat", Path: base, Err: err}
	}

	// Remove all contents will remove the contents of the directory.
	//
	// It was split out of this function to allow the deletion of the
	// contents of a directory, without deleting the directory itself.
	recurseErr := removeContentsFrom(fs, parent, base)

	// Remove the directory itself.
	unlinkErr := fs.unlinkat(parentFd, base, AT_REMOVEDIR)
	if unlinkErr == nil || errors.Is(unlinkErr, ErrNotExist) {
		return nil
	}

	if recurseErr != nil {
		return recurseErr
	}

	return ensurePathError(err, "unlinkat", base)
}

// openFdAt opens path relative to the directory in fd.
// Other than that this should act like openFileNolog.
// This acts like openFileNolog rather than OpenFile because
// we are going to (try to) remove the file.
// The contents of this file are not relevant for test caching.
func openFdAt(dirfd int, name string) (File, error) {
	var fd int
	for {
		var err error
		fd, err = unix.Openat(dirfd, name, O_RDONLY|O_CLOEXEC|O_NOFOLLOW, 0)
		if err == nil {
			break
		}

		// See comment in openFileNolog.
		if err == unix.EINTR {
			continue
		}

		return nil, err
	}
	// This is stupid, os.NewFile immediately casts `fd` to an `int`, but wants
	// it to be passed as a `uintptr`.
	return os.NewFile(uintptr(fd), name), nil
}
