// SPDX-License-Identifier: BSD-3-Clause

// Code in this file was derived from `go/src/io/fs/walk.go`.

// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the `go.LICENSE` file.

package ufs

import (
	iofs "io/fs"
	"path"
)

// SkipDir is used as a return value from [WalkDirFunc] to indicate that
// the directory named in the call is to be skipped. It is not returned
// as an error by any function.
var SkipDir = iofs.SkipDir

// SkipAll is used as a return value from [WalkDirFunc] to indicate that
// all remaining files and directories are to be skipped. It is not returned
// as an error by any function.
var SkipAll = iofs.SkipAll

// WalkDirFunc is the type of the function called by [WalkDir] to visit
// each file or directory.
//
// The path argument contains the argument to [WalkDir] as a prefix.
// That is, if WalkDir is called with root argument "dir" and finds a file
// named "a" in that directory, the walk function will be called with
// argument "dir/a".
//
// The d argument is the [DirEntry] for the named path.
//
// The error result returned by the function controls how [WalkDir]
// continues. If the function returns the special value [SkipDir], WalkDir
// skips the current directory (path if d.IsDir() is true, otherwise
// path's parent directory). If the function returns the special value
// [SkipAll], WalkDir skips all remaining files and directories. Otherwise,
// if the function returns a non-nil error, WalkDir stops entirely and
// returns that error.
//
// The err argument reports an error related to path, signaling that
// [WalkDir] will not walk into that directory. The function can decide how
// to handle that error; as described earlier, returning the error will
// cause WalkDir to stop walking the entire tree.
//
// [WalkDir] calls the function with a non-nil err argument in two cases.
//
// First, if the initial [Stat] on the root directory fails, WalkDir
// calls the function with path set to root, d set to nil, and err set to
// the error from [fs.Stat].
//
// Second, if a directory's ReadDir method (see [ReadDirFile]) fails, WalkDir calls the
// function with path set to the directory's path, d set to an
// [DirEntry] describing the directory, and err set to the error from
// ReadDir. In this second case, the function is called twice with the
// path of the directory: the first call is before the directory read is
// attempted and has err set to nil, giving the function a chance to
// return [SkipDir] or [SkipAll] and avoid the ReadDir entirely. The second call
// is after a failed ReadDir and reports the error from ReadDir.
// (If ReadDir succeeds, there is no second call.)
type WalkDirFunc func(path string, d DirEntry, err error) error

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
func WalkDir(fs Filesystem, root string, fn WalkDirFunc) error {
	info, err := fs.Stat(root)
	if err != nil {
		err = fn(root, nil, err)
	} else {
		err = walkDir(fs, root, iofs.FileInfoToDirEntry(info), fn)
	}
	if err == SkipDir || err == SkipAll {
		return nil
	}
	return err
}

// walkDir recursively descends path, calling walkDirFn.
func walkDir(fs Filesystem, name string, d DirEntry, walkDirFn WalkDirFunc) error {
	if err := walkDirFn(name, d, nil); err != nil || !d.IsDir() {
		if err == SkipDir && d.IsDir() {
			// Successfully skipped directory.
			err = nil
		}
		return err
	}

	dirs, err := fs.ReadDir(name)
	if err != nil {
		// Second call, to report ReadDir error.
		err = walkDirFn(name, d, err)
		if err != nil {
			if err == SkipDir && d.IsDir() {
				err = nil
			}
			return err
		}
	}

	for _, d1 := range dirs {
		name1 := path.Join(name, d1.Name())
		if err := walkDir(fs, name1, d1, walkDirFn); err != nil {
			if err == SkipDir {
				break
			}
			return err
		}
	}

	return nil
}
