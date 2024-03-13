// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: Copyright (c) 2024 Matthew Penner

package ufs

import (
	"time"
)

// Filesystem represents a filesystem capable of performing I/O operations.
type Filesystem interface {
	// Chmod changes the mode of the named file to mode.
	//
	// If the file is a symbolic link, it changes the mode of the link's target.
	// If there is an error, it will be of type *PathError.
	//
	// A different subset of the mode bits are used, depending on the
	// operating system.
	//
	// On Unix, the mode's permission bits, ModeSetuid, ModeSetgid, and
	// ModeSticky are used.
	//
	// On Windows, only the 0200 bit (owner writable) of mode is used; it
	// controls whether the file's read-only attribute is set or cleared.
	// The other bits are currently unused. For compatibility with Go 1.12
	// and earlier, use a non-zero mode. Use mode 0400 for a read-only
	// file and 0600 for a readable+writable file.
	//
	// On Plan 9, the mode's permission bits, ModeAppend, ModeExclusive,
	// and ModeTemporary are used.
	Chmod(name string, mode FileMode) error

	// Chown changes the numeric uid and gid of the named file.
	//
	// If the file is a symbolic link, it changes the uid and gid of the link's target.
	// A uid or gid of -1 means to not change that value.
	// If there is an error, it will be of type *PathError.
	//
	// On Windows or Plan 9, Chown always returns the syscall.EWINDOWS or
	// EPLAN9 error, wrapped in *PathError.
	Chown(name string, uid, gid int) error

	// Lchown changes the numeric uid and gid of the named file.
	//
	// If the file is a symbolic link, it changes the uid and gid of the link itself.
	// If there is an error, it will be of type *PathError.
	//
	// On Windows, it always returns the syscall.EWINDOWS error, wrapped
	// in *PathError.
	Lchown(name string, uid, gid int) error

	// Chtimes changes the access and modification times of the named
	// file, similar to the Unix utime() or utimes() functions.
	//
	// The underlying filesystem may truncate or round the values to a
	// less precise time unit.
	//
	// If there is an error, it will be of type *PathError.
	Chtimes(name string, atime, mtime time.Time) error

	// Create creates or truncates the named file. If the file already exists,
	// it is truncated.
	//
	// If the file does not exist, it is created with mode 0666
	// (before umask). If successful, methods on the returned File can
	// be used for I/O; the associated file descriptor has mode O_RDWR.
	// If there is an error, it will be of type *PathError.
	Create(name string) (File, error)

	// Mkdir creates a new directory with the specified name and permission
	// bits (before umask).
	//
	// If there is an error, it will be of type *PathError.
	Mkdir(name string, perm FileMode) error

	// MkdirAll creates a directory named path, along with any necessary
	// parents, and returns nil, or else returns an error.
	//
	// The permission bits perm (before umask) are used for all
	// directories that MkdirAll creates.
	// If path is already a directory, MkdirAll does nothing
	// and returns nil.
	MkdirAll(path string, perm FileMode) error

	// Open opens the named file for reading.
	//
	// If successful, methods on the returned file can be used for reading; the
	// associated file descriptor has mode O_RDONLY.
	//
	// If there is an error, it will be of type *PathError.
	Open(name string) (File, error)

	// OpenFile is the generalized open call; most users will use Open
	// or Create instead. It opens the named file with specified flag
	// (O_RDONLY etc.).
	//
	// If the file does not exist, and the O_CREATE flag
	// is passed, it is created with mode perm (before umask). If successful,
	// methods on the returned File can be used for I/O.
	//
	// If there is an error, it will be of type *PathError.
	OpenFile(name string, flag int, perm FileMode) (File, error)

	// ReadDir reads the named directory,
	//
	// returning all its directory entries sorted by filename.
	// If an error occurs reading the directory, ReadDir returns the entries it
	// was able to read before the error, along with the error.
	ReadDir(name string) ([]DirEntry, error)

	// Remove removes the named file or (empty) directory.
	//
	// If there is an error, it will be of type *PathError.
	Remove(name string) error

	// RemoveAll removes path and any children it contains.
	//
	// It removes everything it can but returns the first error
	// it encounters. If the path does not exist, RemoveAll
	// returns nil (no error).
	//
	// If there is an error, it will be of type *PathError.
	RemoveAll(path string) error

	// Rename renames (moves) oldpath to newpath.
	//
	// If newpath already exists and is not a directory, Rename replaces it.
	// OS-specific restrictions may apply when oldpath and newpath are in different directories.
	// Even within the same directory, on non-Unix platforms Rename is not an atomic operation.
	//
	// If there is an error, it will be of type *LinkError.
	Rename(oldname, newname string) error

	// Stat returns a FileInfo describing the named file.
	//
	// If there is an error, it will be of type *PathError.
	Stat(name string) (FileInfo, error)

	// Lstat returns a FileInfo describing the named file.
	//
	// If the file is a symbolic link, the returned FileInfo
	// describes the symbolic link. Lstat makes no attempt to follow the link.
	//
	// If there is an error, it will be of type *PathError.
	Lstat(name string) (FileInfo, error)

	// Symlink creates newname as a symbolic link to oldname.
	//
	// On Windows, a symlink to a non-existent oldname creates a file symlink;
	// if oldname is later created as a directory the symlink will not work.
	//
	// If there is an error, it will be of type *LinkError.
	Symlink(oldname, newname string) error

	// WalkDir walks the file tree rooted at root, calling fn for each file or
	// directory in the tree, including root.
	//
	// All errors that arise visiting files and directories are filtered by fn:
	// see the [WalkDirFunc] documentation for details.
	//
	// The files are walked in lexical order, which makes the output deterministic
	// but requires WalkDir to read an entire directory into memory before proceeding
	// to walk that directory.
	//
	// WalkDir does not follow symbolic links found in directories,
	// but if root itself is a symbolic link, its target will be walked.
	WalkDir(root string, fn WalkDirFunc) error
}
