// SPDX-License-Identifier: BSD-3-Clause

// Code in this file was copied from `go/src/os/stat_linux.go`
// and `go/src/os/types_unix.go`.

// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the `go.LICENSE` file.

//go:build unix

package ufs

import (
	"time"

	"golang.org/x/sys/unix"
)

type fileStat struct {
	name    string
	size    int64
	mode    FileMode
	modTime time.Time
	sys     unix.Stat_t
}

var _ FileInfo = (*fileStat)(nil)

func (fs *fileStat) Size() int64        { return fs.size }
func (fs *fileStat) Mode() FileMode     { return fs.mode }
func (fs *fileStat) ModTime() time.Time { return fs.modTime }
func (fs *fileStat) Sys() any           { return &fs.sys }
func (fs *fileStat) Name() string       { return fs.name }
func (fs *fileStat) IsDir() bool        { return fs.Mode().IsDir() }

func fillFileStatFromSys(fs *fileStat, name string) {
	fs.name = basename(name)
	fs.size = fs.sys.Size
	fs.modTime = time.Unix(fs.sys.Mtim.Unix())
	fs.mode = FileMode(fs.sys.Mode & 0o777)
	switch fs.sys.Mode & unix.S_IFMT {
	case unix.S_IFBLK:
		fs.mode |= ModeDevice
	case unix.S_IFCHR:
		fs.mode |= ModeDevice | ModeCharDevice
	case unix.S_IFDIR:
		fs.mode |= ModeDir
	case unix.S_IFIFO:
		fs.mode |= ModeNamedPipe
	case unix.S_IFLNK:
		fs.mode |= ModeSymlink
	case unix.S_IFREG:
		// nothing to do
	case unix.S_IFSOCK:
		fs.mode |= ModeSocket
	}
	if fs.sys.Mode&unix.S_ISGID != 0 {
		fs.mode |= ModeSetgid
	}
	if fs.sys.Mode&unix.S_ISUID != 0 {
		fs.mode |= ModeSetuid
	}
	if fs.sys.Mode&unix.S_ISVTX != 0 {
		fs.mode |= ModeSticky
	}
}
