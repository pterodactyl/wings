// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: Copyright (c) 2024 Matthew Penner

//go:build unix

package ufs

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

// UnixFS is a filesystem that uses the unix package to make io calls.
//
// This is used for proper sand-boxing and full control over the exact syscalls
// being performed.
type UnixFS struct {
	// basePath is the base path for file operations to take place in.
	basePath string

	// useOpenat2 controls whether the `openat2` syscall is used instead of the
	// older `openat` syscall.
	useOpenat2 bool
}

// NewUnixFS creates a new sandboxed unix filesystem. BasePath is used as the
// sandbox path, operations on BasePath itself are not allowed, but any
// operations on its descendants are. Symlinks pointing outside BasePath are
// checked and prevented from enabling an escape in a non-raceable manor.
func NewUnixFS(basePath string, useOpenat2 bool) (*UnixFS, error) {
	basePath = strings.TrimSuffix(basePath, "/")
	fs := &UnixFS{
		basePath:   basePath,
		useOpenat2: useOpenat2,
	}
	return fs, nil
}

// BasePath returns the base path of the UnixFS sandbox, file operations
// pointing outside this path are prohibited and will be blocked by all
// operations implemented by UnixFS.
func (fs *UnixFS) BasePath() string {
	return fs.basePath
}

// Close releases the file descriptor used to sandbox operations within the
// base path of the filesystem.
func (fs *UnixFS) Close() error {
	return nil
}

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
func (fs *UnixFS) Chmod(name string, mode FileMode) error {
	dirfd, name, closeFd, err := fs.safePath(name)
	defer closeFd()
	if err != nil {
		return err
	}
	return fs.fchmodat("chmod", dirfd, name, mode)
}

// Chmodat is like Chmod but it takes a dirfd and name instead of a full path.
func (fs *UnixFS) Chmodat(dirfd int, name string, mode FileMode) error {
	return fs.fchmodat("chmodat", dirfd, name, mode)
}

func (fs *UnixFS) fchmodat(op string, dirfd int, name string, mode FileMode) error {
	return ensurePathError(unix.Fchmodat(dirfd, name, uint32(mode), 0), op, name)
}

// Chown changes the numeric uid and gid of the named file.
//
// If the file is a symbolic link, it changes the uid and gid of the link's target.
// A uid or gid of -1 means to not change that value.
// If there is an error, it will be of type *PathError.
//
// On Windows or Plan 9, Chown always returns the syscall.EWINDOWS or
// EPLAN9 error, wrapped in *PathError.
func (fs *UnixFS) Chown(name string, uid, gid int) error {
	return ensurePathError(fs.fchown(name, uid, gid, 0), "chown", name)
}

// Lchown changes the numeric uid and gid of the named file.
//
// If the file is a symbolic link, it changes the uid and gid of the link itself.
// If there is an error, it will be of type *PathError.
//
// On Windows, it always returns the syscall.EWINDOWS error, wrapped
// in *PathError.
func (fs *UnixFS) Lchown(name string, uid, gid int) error {
	// With AT_SYMLINK_NOFOLLOW, Fchownat acts like Lchown but allows us to
	// pass a dirfd.
	return ensurePathError(fs.fchown(name, uid, gid, AT_SYMLINK_NOFOLLOW), "lchown", name)
}

// fchown is a re-usable Fchownat syscall used by Chown and Lchown.
func (fs *UnixFS) fchown(name string, uid, gid, flags int) error {
	dirfd, name, closeFd, err := fs.safePath(name)
	defer closeFd()
	if err != nil {
		return err
	}
	return unix.Fchownat(dirfd, name, uid, gid, flags)
}

// Chownat is like Chown but allows passing an existing directory file
// descriptor rather than needing to resolve one.
func (fs *UnixFS) Chownat(dirfd int, name string, uid, gid int) error {
	return ensurePathError(unix.Fchownat(dirfd, name, uid, gid, 0), "chownat", name)
}

// Lchownat is like Lchown but allows passing an existing directory file
// descriptor rather than needing to resolve one.
func (fs *UnixFS) Lchownat(dirfd int, name string, uid, gid int) error {
	return ensurePathError(unix.Fchownat(dirfd, name, uid, gid, AT_SYMLINK_NOFOLLOW), "lchownat", name)
}

// Chtimes changes the access and modification times of the named
// file, similar to the Unix utime() or utimes() functions.
//
// The underlying filesystem may truncate or round the values to a
// less precise time unit.
//
// If there is an error, it will be of type *PathError.
func (fs *UnixFS) Chtimes(name string, atime, mtime time.Time) error {
	dirfd, name, closeFd, err := fs.safePath(name)
	defer closeFd()
	if err != nil {
		return err
	}
	return fs.Chtimesat(dirfd, name, atime, mtime)
}

// Chtimesat is like Chtimes but allows passing an existing directory file
// descriptor rather than needing to resolve one.
func (fs *UnixFS) Chtimesat(dirfd int, name string, atime, mtime time.Time) error {
	var utimes [2]unix.Timespec
	set := func(i int, t time.Time) {
		if t.IsZero() {
			utimes[i] = unix.Timespec{Sec: unix.UTIME_OMIT, Nsec: unix.UTIME_OMIT}
		} else {
			utimes[i] = unix.NsecToTimespec(t.UnixNano())
		}
	}
	set(0, atime)
	set(1, mtime)

	// This does support `AT_SYMLINK_NOFOLLOW` as well if needed.
	return ensurePathError(unix.UtimesNanoAt(dirfd, name, utimes[0:], 0), "chtimes", name)
}

// Create creates or truncates the named file. If the file already exists,
// it is truncated.
//
// If the file does not exist, it is created with mode 0666
// (before umask). If successful, methods on the returned File can
// be used for I/O; the associated file descriptor has mode O_RDWR.
// If there is an error, it will be of type *PathError.
func (fs *UnixFS) Create(name string) (File, error) {
	return fs.OpenFile(name, O_CREATE|O_WRONLY|O_TRUNC, 0o644)
}

// Mkdir creates a new directory with the specified name and permission
// bits (before umask).
//
// If there is an error, it will be of type *PathError.
func (fs *UnixFS) Mkdir(name string, mode FileMode) error {
	dirfd, name, closeFd, err := fs.safePath(name)
	defer closeFd()
	if err != nil {
		return err
	}
	return fs.mkdirat("mkdir", dirfd, name, mode)
}

func (fs *UnixFS) Mkdirat(dirfd int, name string, mode FileMode) error {
	return fs.mkdirat("mkdirat", dirfd, name, mode)
}

func (fs *UnixFS) mkdirat(op string, dirfd int, name string, mode FileMode) error {
	return ensurePathError(unix.Mkdirat(dirfd, name, uint32(mode)), op, name)
}

// MkdirAll creates a directory named path, along with any necessary
// parents, and returns nil, or else returns an error.
//
// The permission bits perm (before umask) are used for all
// directories that MkdirAll creates.
// If path is already a directory, MkdirAll does nothing
// and returns nil.
func (fs *UnixFS) MkdirAll(name string, mode FileMode) error {
	// Ensure name is somewhat clean before continuing.
	name, err := fs.unsafePath(name)
	if err != nil {
		return err
	}
	return fs.mkdirAll(name, mode)
}

// Open opens the named file for reading.
//
// If successful, methods on the returned file can be used for reading; the
// associated file descriptor has mode O_RDONLY.
//
// If there is an error, it will be of type *PathError.
func (fs *UnixFS) Open(name string) (File, error) {
	return fs.OpenFile(name, O_RDONLY, 0)
}

// OpenFile is the generalized open call; most users will use Open
// or Create instead. It opens the named file with specified flag
// (O_RDONLY etc.).
//
// If the file does not exist, and the O_CREATE flag
// is passed, it is created with mode perm (before umask). If successful,
// methods on the returned File can be used for I/O.
//
// If there is an error, it will be of type *PathError.
func (fs *UnixFS) OpenFile(name string, flag int, mode FileMode) (File, error) {
	fd, err := fs.openFile(name, flag, mode)
	if err != nil {
		return nil, err
	}
	// Do not close `fd` here, it is passed to a file that needs the fd, the
	// caller of this function is responsible for calling Close() on the File
	// to release the file descriptor.
	return os.NewFile(uintptr(fd), name), nil
}

func (fs *UnixFS) openFile(name string, flag int, mode FileMode) (int, error) {
	dirfd, name, closeFd, err := fs.safePath(name)
	defer closeFd()
	if err != nil {
		return 0, err
	}
	return fs.openat(dirfd, name, flag, mode)
}

func (fs *UnixFS) OpenFileat(dirfd int, name string, flag int, mode FileMode) (File, error) {
	fd, err := fs.openat(dirfd, name, flag, mode)
	if err != nil {
		return nil, err
	}
	// Do not close `fd` here, it is passed to a file that needs the fd, the
	// caller of this function is responsible for calling Close() on the File
	// to release the file descriptor.
	return os.NewFile(uintptr(fd), name), nil
}

// ReadDir reads the named directory,
//
// returning all its directory entries sorted by filename.
// If an error occurs reading the directory, ReadDir returns the entries it
// was able to read before the error, along with the error.
func (fs *UnixFS) ReadDir(path string) ([]DirEntry, error) {
	dirfd, name, closeFd, err := fs.safePath(path)
	defer closeFd()
	if err != nil {
		return nil, err
	}
	fd, err := fs.openat(dirfd, name, O_DIRECTORY|O_RDONLY, 0)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = unix.Close(fd)
	}()
	return fs.readDir(fd, name, ".", nil)
}

// RemoveStat is a combination of Stat and Remove, it is used to more
// efficiently remove a file when the caller needs to stat it before
// removing it.
//
// This optimized function exists for our QuotaFS abstraction, which needs
// to track writes to a filesystem. When removing a file, the QuotaFS needs
// to know if the entry is a file and if so, how large it is. Because we
// need to Stat a file in order to get its mode and size, we will already
// know if the entry needs to be removed by using Unlink or Rmdir. The
// standard `Remove` method just tries both Unlink and Rmdir (in that order)
// as it ends up usually being faster and more efficient than calling Stat +
// the proper operation in the first place.
func (fs *UnixFS) RemoveStat(name string) (FileInfo, error) {
	dirfd, name, closeFd, err := fs.safePath(name)
	defer closeFd()
	if err != nil {
		return nil, err
	}

	// Lstat name, we use Lstat as Unlink doesn't care about symlinks.
	s, err := fs.Lstatat(dirfd, name)
	if err != nil {
		return nil, err
	}

	if s.IsDir() {
		err = fs.unlinkat(dirfd, name, AT_REMOVEDIR) // Rmdir
	} else {
		err = fs.unlinkat(dirfd, name, 0)
	}
	if err != nil {
		return s, ensurePathError(err, "rename", name)
	}
	return s, nil
}

// Remove removes the named file or (empty) directory.
//
// If there is an error, it will be of type *PathError.
func (fs *UnixFS) Remove(name string) error {
	dirfd, name, closeFd, err := fs.safePath(name)
	defer closeFd()
	if err != nil {
		return err
	}

	// Prevent trying to Remove the base directory.
	if name == "." {
		return &PathError{
			Op:   "remove",
			Path: name,
			Err:  ErrBadPathResolution,
		}
	}

	// System call interface forces us to know
	// whether name is a file or directory.
	// Try both: it is cheaper on average than
	// doing a Stat plus the right one.
	err = fs.unlinkat(dirfd, name, 0)
	if err == nil {
		return nil
	}
	err1 := fs.unlinkat(dirfd, name, AT_REMOVEDIR) // Rmdir
	if err1 == nil {
		return nil
	}

	// Both failed: figure out which error to return.
	// OS X and Linux differ on whether unlink(dir)
	// returns EISDIR, so can't use that. However,
	// both agree that rmdir(file) returns ENOTDIR,
	// so we can use that to decide which error is real.
	// Rmdir might also return ENOTDIR if given a bad
	// file path, like /etc/passwd/foo, but in that case,
	// both errors will be ENOTDIR, so it's okay to
	// use the error from unlink.
	if err1 != unix.ENOTDIR {
		err = err1
	}
	return ensurePathError(err, "remove", name)
}

// RemoveAll removes path and any children it contains.
//
// It removes everything it can but returns the first error
// it encounters. If the path does not exist, RemoveAll
// returns nil (no error).
//
// If there is an error, it will be of type *PathError.
func (fs *UnixFS) RemoveAll(name string) error {
	name, err := fs.unsafePath(name)
	if err != nil {
		return err
	}

	// While removeAll internally checks this, I want to make sure we check it
	// and return the proper error so our tests can ensure that this will never
	// be a possibility.
	if name == "." {
		return &PathError{
			Op:   "removeall",
			Path: name,
			Err:  ErrBadPathResolution,
		}
	}

	return fs.removeAll(name)
}

// RemoveContents recursively removes the contents of name.
//
// It removes everything it can but returns the first error
// it encounters. If the path does not exist, RemoveContents
// returns nil (no error).
//
// If there is an error, it will be of type [*PathError].
func (fs *UnixFS) RemoveContents(name string) error {
	name, err := fs.unsafePath(name)
	if err != nil {
		return err
	}

	// Unlike RemoveAll, we don't remove `name` itself, only it's contents.
	// So there is no need to check for a name of `.` here.

	return fs.removeContents(name)
}

func (fs *UnixFS) unlinkat(dirfd int, name string, flags int) error {
	return ignoringEINTR(func() error {
		return unix.Unlinkat(dirfd, name, flags)
	})
}

// Rename renames (moves) oldpath to newpath.
//
// If newpath already exists and is not a directory, Rename replaces it.
// OS-specific restrictions may apply when oldpath and newpath are in different directories.
// Even within the same directory, on non-Unix platforms Rename is not an atomic operation.
//
// If there is an error, it will be of type *LinkError.
func (fs *UnixFS) Rename(oldpath, newpath string) error {
	// Simple case: both paths are the same.
	if oldpath == newpath {
		return nil
	}

	olddirfd, oldname, closeFd, err := fs.safePath(oldpath)
	defer closeFd()
	if err != nil {
		return err
	}
	// Ensure that we are not trying to rename the base directory itself.
	// While unix.Renameat ends up throwing a "device or resource busy" error,
	// that doesn't mean we are protecting the system properly.
	if oldname == "." {
		return &PathError{
			Op:   "rename",
			Path: oldname,
			Err:  ErrBadPathResolution,
		}
	}
	// Stat the old target to return proper errors.
	if _, err := fs.Lstatat(olddirfd, oldname); err != nil {
		return err
	}

	newdirfd, newname, closeFd2, err := fs.safePath(newpath)
	if err != nil {
		closeFd2()
		if !errors.Is(err, ErrNotExist) {
			return err
		}
		var pathErr *PathError
		if !errors.As(err, &pathErr) {
			return err
		}
		if err := fs.MkdirAll(pathErr.Path, 0o755); err != nil {
			return err
		}
		newdirfd, newname, closeFd2, err = fs.safePath(newpath)
		defer closeFd2()
		if err != nil {
			return err
		}
	} else {
		defer closeFd2()
	}

	// Ensure that we are not trying to rename the base directory itself.
	// While unix.Renameat ends up throwing a "device or resource busy" error,
	// that doesn't mean we are protecting the system properly.
	if newname == "." {
		return &PathError{
			Op:   "rename",
			Path: newname,
			Err:  ErrBadPathResolution,
		}
	}
	// Stat the new target to return proper errors.
	_, err = fs.Lstatat(newdirfd, newname)
	switch {
	case err == nil:
		return &PathError{
			Op:   "rename",
			Path: newname,
			Err:  ErrExist,
		}
	case !errors.Is(err, ErrNotExist):
		return err
	}
	if err := unix.Renameat(olddirfd, oldname, newdirfd, newname); err != nil {
		return &LinkError{Op: "rename", Old: oldpath, New: newpath, Err: err}
	}
	return nil
}

// Stat returns a FileInfo describing the named file.
//
// If there is an error, it will be of type *PathError.
func (fs *UnixFS) Stat(name string) (FileInfo, error) {
	return fs._fstat("stat", name, 0)
}

// Statat is like Stat but allows passing an existing directory file
// descriptor rather than needing to resolve one.
func (fs *UnixFS) Statat(dirfd int, name string) (FileInfo, error) {
	return fs._fstatat("statat", dirfd, name, 0)
}

// Lstat returns a FileInfo describing the named file.
//
// If the file is a symbolic link, the returned FileInfo
// describes the symbolic link. Lstat makes no attempt to follow the link.
//
// If there is an error, it will be of type *PathError.
func (fs *UnixFS) Lstat(name string) (FileInfo, error) {
	return fs._fstat("lstat", name, AT_SYMLINK_NOFOLLOW)
}

// Lstatat is like Lstat but allows passing an existing directory file
// descriptor rather than needing to resolve one.
func (fs *UnixFS) Lstatat(dirfd int, name string) (FileInfo, error) {
	return fs._fstatat("lstatat", dirfd, name, AT_SYMLINK_NOFOLLOW)
}

func (fs *UnixFS) fstat(name string, flags int) (FileInfo, error) {
	return fs._fstat("fstat", name, flags)
}

func (fs *UnixFS) _fstat(op string, name string, flags int) (FileInfo, error) {
	dirfd, name, closeFd, err := fs.safePath(name)
	defer closeFd()
	if err != nil {
		return nil, err
	}
	return fs._fstatat(op, dirfd, name, flags)
}

func (fs *UnixFS) fstatat(dirfd int, name string, flags int) (FileInfo, error) {
	return fs._fstatat("fstatat", dirfd, name, flags)
}

func (fs *UnixFS) _fstatat(op string, dirfd int, name string, flags int) (FileInfo, error) {
	var s fileStat
	if err := ignoringEINTR(func() error {
		return unix.Fstatat(dirfd, name, &s.sys, flags)
	}); err != nil {
		return nil, ensurePathError(err, op, name)
	}
	fillFileStatFromSys(&s, name)
	return &s, nil
}

// Symlink creates newname as a symbolic link to oldname.
//
// On Windows, a symlink to a non-existent oldname creates a file symlink;
// if oldname is later created as a directory the symlink will not work.
//
// If there is an error, it will be of type *LinkError.
func (fs *UnixFS) Symlink(oldpath, newpath string) error {
	dirfd, newpath, closeFd, err := fs.safePath(newpath)
	defer closeFd()
	if err != nil {
		return err
	}
	if err := ignoringEINTR(func() error {
		// We aren't concerned with oldpath here as a symlink can point anywhere
		// it wants.
		return unix.Symlinkat(oldpath, dirfd, newpath)
	}); err != nil {
		return &LinkError{Op: "symlink", Old: oldpath, New: newpath, Err: err}
	}
	return nil
}

// Touch will attempt to open a file for reading and/or writing. If the file
// does not exist it will be created, and any missing parent directories will
// also be created. The opened file may be truncated, only if `flag` has
// O_TRUNC set.
func (fs *UnixFS) Touch(path string, flag int, mode FileMode) (File, error) {
	if flag&O_CREATE == 0 {
		flag |= O_CREATE
	}
	dirfd, name, closeFd, err, _ := fs.TouchPath(path)
	defer closeFd()
	if err != nil {
		return nil, err
	}
	return fs.OpenFileat(dirfd, name, flag, mode)
}

// TouchPath is like SafePath except that it will create any missing directories
// in the path. Unlike SafePath, TouchPath returns an additional boolean which
// indicates whether the parent directories already existed, this is intended to
// be used as a way to know if the final destination could already exist.
func (fs *UnixFS) TouchPath(path string) (int, string, func(), error, bool) {
	dirfd, name, closeFd, err := fs.safePath(path)
	switch {
	case err == nil:
		return dirfd, name, closeFd, nil, true
	case !errors.Is(err, ErrNotExist):
		return dirfd, name, closeFd, err, false
	}

	var pathErr *PathError
	if !errors.As(err, &pathErr) {
		return dirfd, name, closeFd, err, false
	}
	if err := fs.MkdirAll(pathErr.Path, 0o755); err != nil {
		return dirfd, name, closeFd, err, false
	}

	// Close the previous file descriptor since we are going to be opening
	// a new one.
	closeFd()

	// Run safe path again now that the parent directories have been created.
	dirfd, name, closeFd, err = fs.safePath(path)
	return dirfd, name, closeFd, err, false
}

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
func (fs *UnixFS) WalkDir(root string, fn WalkDirFunc) error {
	return WalkDir(fs, root, fn)
}

// openat is a wrapper around both unix.Openat and unix.Openat2. If the UnixFS
// was configured to enable openat2 support, unix.Openat2 will be used instead
// of unix.Openat due to having better security properties for our use-case.
func (fs *UnixFS) openat(dirfd int, name string, flag int, mode FileMode) (int, error) {
	if flag&O_NOFOLLOW == 0 {
		flag |= O_NOFOLLOW
	}

	var fd int
	for {
		var err error
		if fs.useOpenat2 {
			fd, err = fs._openat2(dirfd, name, uint64(flag), uint64(syscallMode(mode)))
		} else {
			fd, err = fs._openat(dirfd, name, flag, uint32(syscallMode(mode)))
		}
		if err == nil {
			break
		}
		// We have to check EINTR here, per issues https://go.dev/issue/11180 and https://go.dev/issue/39237.
		if err == unix.EINTR {
			continue
		}
		return 0, err
	}

	// If we are using openat2, we don't need the additional security checks.
	if fs.useOpenat2 {
		return fd, nil
	}

	// If we are not using openat2, do additional path checking. This assumes
	// that openat2 is using `RESOLVE_BENEATH` to avoid the same security
	// issue.
	var finalPath string
	finalPath, err := filepath.EvalSymlinks(filepath.Join("/proc/self/fd/", strconv.Itoa(fd)))
	if err != nil {
		if !errors.Is(err, ErrNotExist) {
			return fd, fmt.Errorf("failed to evaluate symlink: %w", convertErrorType(err))
		}

		// The target of one of the symlinks (EvalSymlinks is recursive)
		// does not exist. So get the path that does not exist and use
		// that for further validation instead.
		var pErr *PathError
		if !errors.As(err, &pErr) {
			return fd, fmt.Errorf("failed to evaluate symlink: %w", convertErrorType(err))
		}

		// Update the final path to whatever directory or path didn't exist while
		// recursing any symlinks.
		finalPath = pErr.Path
		// Ensure the error is wrapped correctly.
		err = convertErrorType(err)
	}

	// Check if the path is within our root.
	if !fs.unsafeIsPathInsideOfBase(finalPath) {
		op := "openat"
		if fs.useOpenat2 {
			op = "openat2"
		}
		return fd, &PathError{
			Op:   op,
			Path: name,
			Err:  ErrBadPathResolution,
		}
	}

	// Return the file descriptor and any potential error.
	return fd, err
}

// _openat is a wrapper around unix.Openat. This method should never be directly
// called, use `openat` instead.
func (fs *UnixFS) _openat(dirfd int, name string, flag int, mode uint32) (int, error) {
	// Ensure the O_CLOEXEC flag is set.
	// Go sets this in the os package, but since we are directly using unix
	// we need to set it ourselves.
	if flag&O_CLOEXEC == 0 {
		flag |= O_CLOEXEC
	}
	// O_LARGEFILE is set by Openat for us automatically.
	fd, err := unix.Openat(dirfd, name, flag, mode)
	switch {
	case err == nil:
		return fd, nil
	case err == unix.EINTR:
		return fd, err
	case err == unix.EAGAIN:
		return fd, err
	default:
		return fd, ensurePathError(err, "openat", name)
	}
}

// _openat2 is a wonderful syscall that supersedes the `openat` syscall. It has
// improved validation and security characteristics that weren't available or
// considered when `openat` was originally implemented. As such, it is only
// present in Kernel 5.6 and above.
//
// This method should never be directly called, use `openat` instead.
func (fs *UnixFS) _openat2(dirfd int, name string, flag, mode uint64) (int, error) {
	// Ensure the O_CLOEXEC flag is set.
	// Go sets this when using the os package, but since we are directly using
	// the unix package we need to set it ourselves.
	if flag&O_CLOEXEC == 0 {
		flag |= O_CLOEXEC
	}
	// Ensure the O_LARGEFILE flag is set.
	// Go sets this for unix.Open, unix.Openat, but not unix.Openat2.
	if flag&O_LARGEFILE == 0 {
		flag |= O_LARGEFILE
	}
	fd, err := unix.Openat2(dirfd, name, &unix.OpenHow{
		Flags: flag,
		Mode:  mode,
		// This is the bread and butter of preventing a symlink escape, without
		// this option, we have to handle path validation fully on our own.
		//
		// This is why using Openat2 over Openat is preferred if available.
		Resolve: unix.RESOLVE_BENEATH,
	})
	switch {
	case err == nil:
		return fd, nil
	case err == unix.EINTR:
		return fd, err
	case err == unix.EAGAIN:
		return fd, err
	default:
		return fd, ensurePathError(err, "openat2", name)
	}
}

func (fs *UnixFS) SafePath(path string) (int, string, func(), error) {
	return fs.safePath(path)
}

func (fs *UnixFS) safePath(path string) (dirfd int, file string, closeFd func(), err error) {
	// Default closeFd to a NO-OP.
	closeFd = func() {}

	// Use unsafePath to clean the path and strip BasePath if path is absolute.
	var name string
	name, err = fs.unsafePath(path)
	if err != nil {
		return
	}

	// Open the base path. We use this as the sandbox root for any further
	// operations.
	var fsDirfd int
	fsDirfd, err = fs._openat(AT_EMPTY_PATH, fs.basePath, O_DIRECTORY|O_RDONLY, 0)
	if err != nil {
		return
	}

	// Split the parent from the last element in the path, this gives us the
	// "file name" and the full path to its parent.
	var dir string
	dir, file = filepath.Split(name)
	// If dir is empty then name is not nested.
	if dir == "" {
		dirfd = fsDirfd
		closeFd = func() { _ = unix.Close(dirfd) }

		// Return dirfd, name, an empty closeFd func, and no error
		return
	}

	// Dir will usually contain a trailing slash as filepath.Split doesn't
	// trim slashes.
	dir = strings.TrimSuffix(dir, "/")
	dirfd, err = fs.openat(fsDirfd, dir, O_DIRECTORY|O_RDONLY, 0)
	if err != nil {
		// An error occurred while opening the directory, but we already opened
		// the filesystem root, so we still need to ensure it gets closed.
		closeFd = func() { _ = unix.Close(fsDirfd) }
	} else {
		// Set closeFd to close the newly opened directory file descriptor.
		closeFd = func() {
			_ = unix.Close(dirfd)
			_ = unix.Close(fsDirfd)
		}
	}

	// Return dirfd, name, the closeFd func, and err
	return
}

// unsafePath strips and joins the given path with the filesystem's base path,
// cleaning the result. The cleaned path is then checked if it starts with the
// filesystem's base path to obvious any obvious path traversal escapes. The
// fully resolved path (if symlinks are followed) may not be within the
// filesystem's base path, additional checks are required to safely use paths
// returned by this function.
func (fs *UnixFS) unsafePath(path string) (string, error) {
	// Calling filepath.Clean on the path will resolve it to it's absolute path,
	// removing any path traversal arguments (such as ..), leaving us with an
	// absolute path we can then use.
	//
	// This will also trim the filesystem's base path from the given path and
	// join the base path back on to ensure the path starts with the base path
	// without appending it twice.
	r := filepath.Clean(filepath.Join(fs.basePath, strings.TrimPrefix(path, fs.basePath)))

	if fs.unsafeIsPathInsideOfBase(r) {
		// This is kinda ironic isn't it.
		// We do this as we are operating with dirfds and `*at` syscalls which
		// behave differently if given an absolute path.
		//
		// First trim the BasePath, then trim any leading slashes.
		r = strings.TrimPrefix(strings.TrimPrefix(r, fs.basePath), "/")
		// If the path is empty then return "." as the path is pointing to the
		// root.
		if r == "" {
			return ".", nil
		}
		return r, nil
	}

	return "", &PathError{
		Op:   "safePath",
		Path: path,
		Err:  ErrBadPathResolution,
	}
}

// unsafeIsPathInsideOfBase checks if the given path is inside the filesystem's
// base path.
//
// NOTE: this method doesn't clean the given path or attempt to join the
// filesystem's base path. This is purely a basic prefix check against the
// given path.
func (fs *UnixFS) unsafeIsPathInsideOfBase(path string) bool {
	return strings.HasPrefix(
		strings.TrimSuffix(path, "/")+"/",
		fs.basePath+"/",
	)
}
