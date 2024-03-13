// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: Copyright (c) 2024 Matthew Penner

package ufs

import (
	"sync/atomic"
)

type Quota struct {
	// fs is the underlying filesystem that runs the actual I/O operations.
	*UnixFS

	// limit is the size limit of the filesystem.
	//
	// limit is atomic to allow the limit to be safely changed after the
	// filesystem was created.
	//
	// A limit of `-1` disables any write operation from being performed.
	// A limit of `0` disables any limit checking.
	limit atomic.Int64

	// usage is the current usage of the filesystem.
	//
	// If usage is set to `-1`, it hasn't been calculated yet.
	usage atomic.Int64
}

func NewQuota(fs *UnixFS, limit int64) *Quota {
	qfs := Quota{UnixFS: fs}
	qfs.limit.Store(limit)
	return &qfs
}

// Close closes the filesystem.
func (fs *Quota) Close() (err error) {
	err = fs.UnixFS.Close()
	return
}

// Limit returns the limit of the filesystem.
func (fs *Quota) Limit() int64 {
	return fs.limit.Load()
}

// SetLimit returns the limit of the filesystem.
func (fs *Quota) SetLimit(newLimit int64) int64 {
	return fs.limit.Swap(newLimit)
}

// Usage returns the current usage of the filesystem.
func (fs *Quota) Usage() int64 {
	return fs.usage.Load()
}

// SetUsage updates the total usage of the filesystem.
func (fs *Quota) SetUsage(newUsage int64) int64 {
	return fs.usage.Swap(newUsage)
}

// Add adds `i` to the tracked usage total.
func (fs *Quota) Add(i int64) int64 {
	usage := fs.Usage()

	// If adding `i` to the usage will put us below 0, cap it. (`i` can be negative)
	if usage+i < 0 {
		fs.usage.Store(0)
		return 0
	}
	return fs.usage.Add(i)
}

// CanFit checks if the given size can fit in the filesystem without exceeding
// the limit of the filesystem.
func (fs *Quota) CanFit(size int64) bool {
	// Get the size limit of the filesystem.
	limit := fs.Limit()
	switch limit {
	case -1:
		// A limit of -1 means no write operations are allowed.
		return false
	case 0:
		// A limit of 0 means unlimited.
		return true
	}

	// Any other limit is a value we need to check.
	usage := fs.Usage()
	if usage == -1 {
		// We don't know what the current usage is yet.
		return true
	}

	// If the current usage + the requested size are under the limit of the
	// filesystem, allow it.
	if usage+size <= limit {
		return true
	}

	// Welp, the size would exceed the limit of the filesystem, deny it.
	return false
}

func (fs *Quota) Remove(name string) error {
	// For information on why this interface is used here, check its
	// documentation.
	s, err := fs.RemoveStat(name)
	if err != nil {
		return err
	}

	// Don't reduce the quota's usage as `name` is not a regular file.
	if !s.Mode().IsRegular() {
		return nil
	}

	// Remove the size of the deleted file from the quota usage.
	fs.Add(-s.Size())
	return nil
}

// RemoveAll removes path and any children it contains.
//
// It removes everything it can but returns the first error
// it encounters. If the path does not exist, RemoveAll
// returns nil (no error).
//
// If there is an error, it will be of type *PathError.
func (fs *Quota) RemoveAll(name string) error {
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

func (fs *Quota) removeAll(path string) error {
	return removeAll(fs, path)
}

func (fs *Quota) unlinkat(dirfd int, name string, flags int) error {
	if flags == 0 {
		s, err := fs.Lstatat(dirfd, name)
		if err == nil && s.Mode().IsRegular() {
			fs.Add(-s.Size())
		}
	}
	return fs.UnixFS.unlinkat(dirfd, name, flags)
}
