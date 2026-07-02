// SPDX-License-Identifier: BSD-3-Clause

// Code in this file was derived from `go/src/os/path.go`.

// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the `go.LICENSE` file.

//go:build unix

package ufs

// mkdirAll is a recursive Mkdir implementation that properly handles symlinks.
//
// It returns the directories it created, ordered from shallowest to deepest, so
// callers can act on exactly the paths that were new (for example to change
// their ownership). Directories that already existed are not included.
func (fs *UnixFS) mkdirAll(name string, mode FileMode) ([]string, error) {
	// Fast path: if we can tell whether path is a directory or file, stop with success or error.
	dir, err := fs.Lstat(name)
	if err == nil {
		if dir.Mode()&ModeSymlink != 0 {
			// If the final path is a symlink, resolve its target and use that
			// to check instead.
			dir, err = fs.Stat(name)
			if err != nil {
				return nil, err
			}
		}
		if dir.IsDir() {
			return nil, nil
		}
		return nil, &PathError{Op: "mkdir", Path: name, Err: ErrNotDirectory}
	}

	// Slow path: make sure parent exists and then call Mkdir for path.
	i := len(name)
	for i > 0 && name[i-1] == '/' { // Skip trailing path separator.
		i--
	}

	j := i
	for j > 0 && name[j-1] != '/' { // Scan backward over element.
		j--
	}

	var created []string
	if j > 1 {
		// Create parent.
		created, err = fs.mkdirAll(name[:j-1], mode)
		if err != nil {
			return created, err
		}
	}

	// Parent now exists; invoke Mkdir and use its result.
	err = fs.Mkdir(name, mode)
	if err != nil {
		// Handle arguments like "foo/." by
		// double-checking that directory doesn't exist.
		dir, err1 := fs.Lstat(name)
		if err1 == nil && dir.IsDir() {
			return created, nil
		}
		return created, err
	}
	return append(created, name), nil
}
