// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: Copyright (c) 2016 Matthew Holt

// Code in this file was derived from
// https://github.com/mholt/archiver/blob/v4.0.0-alpha.8/fs.go
//
// These modifications were necessary to allow us to use an already open file
// with archiver.FileFS.

package archiverext

import (
	"io"
	"io/fs"

	"github.com/mholt/archiver/v4"
)

// FileFS allows accessing a file on disk using a consistent file system interface.
// The value should be the path to a regular file, not a directory. This file will
// be the only entry in the file system and will be at its root. It can be accessed
// within the file system by the name of "." or the filename.
//
// If the file is compressed, set the Compression field so that reads from the
// file will be transparently decompressed.
type FileFS struct {
	// File is the compressed file backing the FileFS.
	File fs.File

	// If file is compressed, setting this field will
	// transparently decompress reads.
	Compression archiver.Decompressor
}

// Open opens the named file, which must be the file used to create the file system.
func (f FileFS) Open(name string) (fs.File, error) {
	if err := f.checkName(name, "open"); err != nil {
		return nil, err
	}
	if f.Compression == nil {
		return f.File, nil
	}
	r, err := f.Compression.OpenReader(f.File)
	if err != nil {
		return nil, err
	}
	return compressedFile{f.File, r}, nil
}

// ReadDir returns a directory listing with the file as the singular entry.
func (f FileFS) ReadDir(name string) ([]fs.DirEntry, error) {
	if err := f.checkName(name, "stat"); err != nil {
		return nil, err
	}
	info, err := f.Stat(name)
	if err != nil {
		return nil, err
	}
	return []fs.DirEntry{fs.FileInfoToDirEntry(info)}, nil
}

// Stat stats the named file, which must be the file used to create the file system.
func (f FileFS) Stat(name string) (fs.FileInfo, error) {
	if err := f.checkName(name, "stat"); err != nil {
		return nil, err
	}
	return f.File.Stat()
}

func (f FileFS) checkName(name, op string) error {
	if !fs.ValidPath(name) {
		return &fs.PathError{Op: "open", Path: name, Err: fs.ErrInvalid}
	}
	// TODO: we may need better name validation.
	if name != "." {
		return &fs.PathError{Op: op, Path: name, Err: fs.ErrNotExist}
	}
	return nil
}

// compressedFile is an fs.File that specially reads
// from a decompression reader, and which closes both
// that reader and the underlying file.
type compressedFile struct {
	fs.File
	decomp io.ReadCloser
}

func (cf compressedFile) Read(p []byte) (int, error) {
	return cf.decomp.Read(p)
}

func (cf compressedFile) Close() error {
	err := cf.File.Close()
	err2 := cf.decomp.Close()
	if err2 != nil && err == nil {
		err = err2
	}
	return err
}
