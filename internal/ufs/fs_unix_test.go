// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: Copyright (c) 2024 Matthew Penner

//go:build unix

package ufs_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/pterodactyl/wings/internal/ufs"
)

type testUnixFS struct {
	*ufs.UnixFS

	TmpDir string
	Root   string
}

func (fs *testUnixFS) Cleanup() {
	_ = fs.Close()
	_ = os.RemoveAll(fs.TmpDir)
}

func newTestUnixFS() (*testUnixFS, error) {
	tmpDir, err := os.MkdirTemp(os.TempDir(), "ufs")
	if err != nil {
		return nil, err
	}
	root := filepath.Join(tmpDir, "root")
	if err := os.Mkdir(root, 0o755); err != nil {
		return nil, err
	}
	// TODO: test both disabled and enabled.
	fs, err := ufs.NewUnixFS(root, false)
	if err != nil {
		return nil, err
	}
	tfs := &testUnixFS{
		UnixFS: fs,
		TmpDir: tmpDir,
		Root:   root,
	}
	return tfs, nil
}

func TestUnixFS_Remove(t *testing.T) {
	t.Parallel()
	fs, err := newTestUnixFS()
	if err != nil {
		t.Fatal(err)
		return
	}
	defer fs.Cleanup()

	t.Run("base directory", func(t *testing.T) {
		// Try to remove the base directory.
		if err := fs.Remove(""); !errors.Is(err, ufs.ErrBadPathResolution) {
			t.Errorf("expected an a bad path resolution error, but got: %v", err)
			return
		}
	})

	t.Run("path traversal", func(t *testing.T) {
		// Try to remove the base directory.
		if err := fs.RemoveAll("../root"); !errors.Is(err, ufs.ErrBadPathResolution) {
			t.Errorf("expected an a bad path resolution error, but got: %v", err)
			return
		}
	})
}

func TestUnixFS_RemoveAll(t *testing.T) {
	t.Parallel()
	fs, err := newTestUnixFS()
	if err != nil {
		t.Fatal(err)
		return
	}
	defer fs.Cleanup()

	t.Run("base directory", func(t *testing.T) {
		// Try to remove the base directory.
		if err := fs.RemoveAll(""); !errors.Is(err, ufs.ErrBadPathResolution) {
			t.Errorf("expected an a bad path resolution error, but got: %v", err)
			return
		}
	})

	t.Run("path traversal", func(t *testing.T) {
		// Try to remove the base directory.
		if err := fs.RemoveAll("../root"); !errors.Is(err, ufs.ErrBadPathResolution) {
			t.Errorf("expected an a bad path resolution error, but got: %v", err)
			return
		}
	})
}

func TestUnixFS_Rename(t *testing.T) {
	t.Parallel()
	fs, err := newTestUnixFS()
	if err != nil {
		t.Fatal(err)
		return
	}
	defer fs.Cleanup()

	t.Run("rename base directory", func(t *testing.T) {
		// Try to rename the base directory.
		if err := fs.Rename("", "yeet"); !errors.Is(err, ufs.ErrBadPathResolution) {
			t.Errorf("expected an a bad path resolution error, but got: %v", err)
			return
		}
	})

	t.Run("rename over base directory", func(t *testing.T) {
		// Create a directory that we are going to try and move over top of the
		// existing base directory.
		if err := fs.Mkdir("overwrite_dir", 0o755); err != nil {
			t.Error(err)
			return
		}

		// Try to rename over the base directory.
		if err := fs.Rename("overwrite_dir", ""); !errors.Is(err, ufs.ErrBadPathResolution) {
			t.Errorf("expected an a bad path resolution error, but got: %v", err)
			return
		}
	})

	t.Run("directory rename", func(t *testing.T) {
		// Create a directory to rename to something else.
		if err := fs.Mkdir("test_directory", 0o755); err != nil {
			t.Error(err)
			return
		}

		// Try to rename "test_directory" to "directory".
		if err := fs.Rename("test_directory", "directory"); err != nil {
			t.Errorf("expected no error, but got: %v", err)
			return
		}

		// Sanity check
		if _, err := os.Lstat(filepath.Join(fs.Root, "directory")); err != nil {
			t.Errorf("Lstat errored when performing sanity check: %v", err)
			return
		}
	})

	t.Run("file rename", func(t *testing.T) {
		// Create a directory to rename to something else.
		if f, err := fs.Create("test_file"); err != nil {
			t.Error(err)
			return
		} else {
			_ = f.Close()
		}

		// Try to rename "test_file" to "file".
		if err := fs.Rename("test_file", "file"); err != nil {
			t.Errorf("expected no error, but got: %v", err)
			return
		}

		// Sanity check
		if _, err := os.Lstat(filepath.Join(fs.Root, "file")); err != nil {
			t.Errorf("Lstat errored when performing sanity check: %v", err)
			return
		}
	})
}

func TestUnixFS_Touch(t *testing.T) {
	t.Parallel()
	fs, err := newTestUnixFS()
	if err != nil {
		t.Fatal(err)
		return
	}
	defer fs.Cleanup()

	t.Run("base directory", func(t *testing.T) {
		path := "i_touched_a_file"
		f, err := fs.Touch(path, ufs.O_RDWR, 0o644)
		if err != nil {
			t.Error(err)
			return
		}
		_ = f.Close()

		// Sanity check
		if _, err := os.Lstat(filepath.Join(fs.Root, path)); err != nil {
			t.Errorf("Lstat errored when performing sanity check: %v", err)
			return
		}
	})

	t.Run("existing parent directory", func(t *testing.T) {
		dir := "some_parent_directory"
		if err := fs.Mkdir(dir, 0o755); err != nil {
			t.Errorf("error creating parent directory: %v", err)
			return
		}
		path := filepath.Join(dir, "i_touched_a_file")
		f, err := fs.Touch(path, ufs.O_RDWR, 0o644)
		if err != nil {
			t.Errorf("error touching file: %v", err)
			return
		}
		_ = f.Close()

		// Sanity check
		if _, err := os.Lstat(filepath.Join(fs.Root, path)); err != nil {
			t.Errorf("Lstat errored when performing sanity check: %v", err)
			return
		}
	})

	t.Run("non-existent parent directory", func(t *testing.T) {
		path := "some_other_directory/i_touched_a_file"
		f, err := fs.Touch(path, ufs.O_RDWR, 0o644)
		if err != nil {
			t.Errorf("error touching file: %v", err)
			return
		}
		_ = f.Close()

		// Sanity check
		if _, err := os.Lstat(filepath.Join(fs.Root, path)); err != nil {
			t.Errorf("Lstat errored when performing sanity check: %v", err)
			return
		}
	})

	t.Run("non-existent parent directories", func(t *testing.T) {
		path := "some_other_directory/some_directory/i_touched_a_file"
		f, err := fs.Touch(path, ufs.O_RDWR, 0o644)
		if err != nil {
			t.Errorf("error touching file: %v", err)
			return
		}
		_ = f.Close()

		// Sanity check
		if _, err := os.Lstat(filepath.Join(fs.Root, path)); err != nil {
			t.Errorf("Lstat errored when performing sanity check: %v", err)
			return
		}
	})
}
