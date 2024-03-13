// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: Copyright (c) 2024 Matthew Penner

package ufs

import (
	"io"
	iofs "io/fs"

	"golang.org/x/sys/unix"
)

// DirEntry is an entry read from a directory.
type DirEntry = iofs.DirEntry

// File describes readable and/or writable file from a Filesystem.
type File interface {
	// Name returns the base name of the file.
	Name() string

	// Stat returns the FileInfo structure describing the file.
	// If there is an error, it will be of type *PathError.
	Stat() (FileInfo, error)

	// ReadDir reads the contents of the directory associated with the file f
	// and returns a slice of DirEntry values in directory order.
	// Subsequent calls on the same file will yield later DirEntry records in the directory.
	//
	// If n > 0, ReadDir returns at most n DirEntry records.
	// In this case, if ReadDir returns an empty slice, it will return an error explaining why.
	// At the end of a directory, the error is io.EOF.
	//
	// If n <= 0, ReadDir returns all the DirEntry records remaining in the directory.
	// When it succeeds, it returns a nil error (not io.EOF).
	ReadDir(n int) ([]DirEntry, error)

	// Readdirnames reads the contents of the directory associated with file
	// and returns a slice of up to n names of files in the directory,
	// in directory order. Subsequent calls on the same file will yield
	// further names.
	//
	// If n > 0, Readdirnames returns at most n names. In this case, if
	// Readdirnames returns an empty slice, it will return a non-nil error
	// explaining why. At the end of a directory, the error is io.EOF.
	//
	// If n <= 0, Readdirnames returns all the names from the directory in
	// a single slice. In this case, if Readdirnames succeeds (reads all
	// the way to the end of the directory), it returns the slice and a
	// nil error. If it encounters an error before the end of the
	// directory, Readdirnames returns the names read until that point and
	// a non-nil error.
	Readdirnames(n int) (names []string, err error)

	// Fd returns the integer Unix file descriptor referencing the open file.
	// If f is closed, the file descriptor becomes invalid.
	// If f is garbage collected, a finalizer may close the file descriptor,
	// making it invalid; see runtime.SetFinalizer for more information on when
	// a finalizer might be run. On Unix systems this will cause the SetDeadline
	// methods to stop working.
	// Because file descriptors can be reused, the returned file descriptor may
	// only be closed through the Close method of f, or by its finalizer during
	// garbage collection. Otherwise, during garbage collection the finalizer
	// may close an unrelated file descriptor with the same (reused) number.
	//
	// As an alternative, see the f.SyscallConn method.
	Fd() uintptr

	// Truncate changes the size of the file.
	// It does not change the I/O offset.
	// If there is an error, it will be of type *PathError.
	Truncate(size int64) error

	io.Closer

	io.Reader
	io.ReaderAt
	io.ReaderFrom

	io.Writer
	io.WriterAt

	io.Seeker
}

// FileInfo describes a file and is returned by Stat and Lstat.
type FileInfo = iofs.FileInfo

// FileMode represents a file's mode and permission bits.
// The bits have the same definition on all systems, so that
// information about files can be moved from one system
// to another portably. Not all bits apply to all systems.
// The only required bit is ModeDir for directories.
type FileMode = iofs.FileMode

// The defined file mode bits are the most significant bits of the FileMode.
// The nine least-significant bits are the standard Unix rwxrwxrwx permissions.
// The values of these bits should be considered part of the public API and
// may be used in wire protocols or disk representations: they must not be
// changed, although new bits might be added.
const (
	// ModeDir represents a directory.
	// d: is a directory
	ModeDir = iofs.ModeDir
	// ModeAppend represents an append-only file.
	// a: append-only
	ModeAppend = iofs.ModeAppend
	// ModeExclusive represents an exclusive file.
	// l: exclusive use
	ModeExclusive = iofs.ModeExclusive
	// ModeTemporary .
	// T: temporary file; Plan 9 only.
	ModeTemporary = iofs.ModeTemporary
	// ModeSymlink .
	// L: symbolic link.
	ModeSymlink = iofs.ModeSymlink
	// ModeDevice .
	// D: device file.
	ModeDevice = iofs.ModeDevice
	// ModeNamedPipe .
	// p: named pipe (FIFO)
	ModeNamedPipe = iofs.ModeNamedPipe
	// ModeSocket .
	// S: Unix domain socket.
	ModeSocket = iofs.ModeSocket
	// ModeSetuid .
	// u: setuid
	ModeSetuid = iofs.ModeSetuid
	// ModeSetgid .
	// g: setgid
	ModeSetgid = iofs.ModeSetgid
	// ModeCharDevice .
	// c: Unix character device, when ModeDevice is set
	ModeCharDevice = iofs.ModeCharDevice
	// ModeSticky .
	// t: sticky
	ModeSticky = iofs.ModeSticky
	// ModeIrregular .
	// ?: non-regular file; nothing else is known about this file.
	ModeIrregular = iofs.ModeIrregular

	// ModeType .
	ModeType = iofs.ModeType

	// ModePerm .
	// Unix permission bits, 0o777.
	ModePerm = iofs.ModePerm
)

const (
	// O_RDONLY opens the file read-only.
	O_RDONLY = unix.O_RDONLY
	// O_WRONLY opens the file write-only.
	O_WRONLY = unix.O_WRONLY
	// O_RDWR opens the file read-write.
	O_RDWR = unix.O_RDWR
	// O_APPEND appends data to the file when writing.
	O_APPEND = unix.O_APPEND
	// O_CREATE creates a new file if it doesn't exist.
	O_CREATE = unix.O_CREAT
	// O_EXCL is used with O_CREATE, file must not exist.
	O_EXCL = unix.O_EXCL
	// O_SYNC open for synchronous I/O.
	O_SYNC = unix.O_SYNC
	// O_TRUNC truncates regular writable file when opened.
	O_TRUNC = unix.O_TRUNC
	// O_DIRECTORY opens a directory only. If the entry is not a directory an
	// error will be returned.
	O_DIRECTORY = unix.O_DIRECTORY
	// O_NOFOLLOW opens the exact path given without following symlinks.
	O_NOFOLLOW  = unix.O_NOFOLLOW
	O_CLOEXEC   = unix.O_CLOEXEC
	O_LARGEFILE = unix.O_LARGEFILE
)

const (
	AT_SYMLINK_NOFOLLOW = unix.AT_SYMLINK_NOFOLLOW
	AT_REMOVEDIR        = unix.AT_REMOVEDIR
	AT_EMPTY_PATH       = unix.AT_EMPTY_PATH
)
