// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: Copyright (c) 2024 Matthew Penner

//go:build unix

package ufs_test

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
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
	// fmt.Println(tmpDir)
	fs, err := ufs.NewUnixFS(root, true)
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

func TestUnixFS(t *testing.T) {
	t.Parallel()

	fs, err := newTestUnixFS()
	if err != nil {
		t.Fatal(err)
		return
	}
	defer fs.Cleanup()

	// Test creating a file within the root.
	_, _, closeFd, err := fs.SafePath("/")
	closeFd()
	if err != nil {
		t.Error(err)
		return
	}

	f, err := fs.Touch("directory/file", ufs.O_RDWR, 0o644)
	if err != nil {
		t.Error(err)
		return
	}
	_ = f.Close()

	// Test creating a file within the root.
	f, err = fs.Create("test")
	if err != nil {
		t.Error(err)
		return
	}
	_ = f.Close()

	// Test stating a file within the root.
	if _, err := fs.Stat("test"); err != nil {
		t.Error(err)
		return
	}

	// Test creating a directory within the root.
	if err := fs.Mkdir("ima_directory", 0o755); err != nil {
		t.Error(err)
		return
	}

	// Test creating a nested directory within the root.
	if err := fs.Mkdir("ima_directory/ima_nother_directory", 0o755); err != nil {
		t.Error(err)
		return
	}

	// Test creating a file inside a directory within the root.
	f, err = fs.Create("ima_directory/ima_file")
	if err != nil {
		t.Error(err)
		return
	}
	_ = f.Close()

	// Test listing directory entries.
	if _, err := fs.ReadDir("ima_directory"); err != nil {
		t.Error(err)
		return
	}

	// Test symlink pointing outside the root.
	if err := os.Symlink(fs.TmpDir, filepath.Join(fs.Root, "ima_bad_link")); err != nil {
		t.Error(err)
		return
	}
	f, err = fs.Create("ima_bad_link/ima_bad_file")
	if err == nil {
		_ = f.Close()
		t.Error("expected an error")
		return
	}
	if err := fs.Mkdir("ima_bad_link/ima_bad_directory", 0o755); err == nil {
		t.Error("expected an error")
		return
	}

	// Test symlink pointing outside the root inside a parent directory.
	if err := fs.Symlink(fs.TmpDir, filepath.Join(fs.Root, "ima_directory/ima_bad_link")); err != nil {
		t.Error(err)
		return
	}
	if err := fs.Mkdir("ima_directory/ima_bad_link/ima_bad_directory", 0o755); err == nil {
		t.Error("expected an error")
		return
	}

	// Test symlink pointing outside the root with a child directory.
	if err := os.Mkdir(filepath.Join(fs.TmpDir, "ima_directory"), 0o755); err != nil {
		t.Error(err)
		return
	}
	f, err = fs.Create("ima_bad_link/ima_directory/ima_bad_file")
	if err == nil {
		_ = f.Close()
		t.Error("expected an error")
		return
	}
	if err := fs.Mkdir("ima_bad_link/ima_directory/ima_bad_directory", 0o755); err == nil {
		t.Error("expected an error")
		return
	}

	if _, err := fs.ReadDir("ima_bad_link/ima_directory"); err == nil {
		t.Error("expected an error")
		return
	}

	// Create multiple nested directories.
	if err := fs.MkdirAll("ima_directory/ima_directory/ima_directory/ima_directory", 0o755); err != nil {
		t.Error(err)
		return
	}
	if _, err := fs.ReadDir("ima_directory/ima_directory"); err != nil {
		t.Error(err)
		return
	}

	// Test creating a directory under a symlink with a pre-existing directory.
	if err := fs.MkdirAll("ima_bad_link/ima_directory/ima_bad_directory/ima_bad_directory", 0o755); err == nil {
		t.Error("expected an error")
		return
	}

	// Test deletion
	if err := fs.Remove("test"); err != nil {
		t.Error(err)
		return
	}
	if err := fs.Remove("ima_bad_link"); err != nil {
		t.Error(err)
		return
	}

	// Test recursive deletion
	if err := fs.RemoveAll("ima_directory"); err != nil {
		t.Error(err)
		return
	}

	// Test recursive deletion underneath a bad symlink
	if err := fs.Mkdir("ima_directory", 0o755); err != nil {
		t.Error(err)
		return
	}
	if err := fs.Symlink(fs.TmpDir, filepath.Join(fs.Root, "ima_directory/ima_bad_link")); err != nil {
		t.Error(err)
		return
	}
	if err := fs.RemoveAll("ima_directory/ima_bad_link/ima_bad_file"); err == nil {
		t.Error("expected an error")
		return
	}

	// This should delete the symlink itself.
	if err := fs.RemoveAll("ima_directory/ima_bad_link"); err != nil {
		t.Error(err)
		return
	}

	//for i := 0; i < 5; i++ {
	//	dirName := "dir" + strconv.Itoa(i)
	//	if err := fs.Mkdir(dirName, 0o755); err != nil {
	//		t.Error(err)
	//		return
	//	}
	//	for j := 0; j < 5; j++ {
	//		f, err := fs.Create(filepath.Join(dirName, "file"+strconv.Itoa(j)))
	//		if err != nil {
	//			t.Error(err)
	//			return
	//		}
	//		_ = f.Close()
	//	}
	//}
	//
	//if err := fs.WalkDir2("", func(fd int, path string, info filesystem.DirEntry, err error) error {
	//	if err != nil {
	//		return err
	//	}
	//	fmt.Println(path)
	//	return nil
	//}); err != nil {
	//	t.Error(err)
	//	return
	//}
}

func TestUnixFS_Chmod(t *testing.T) {
	t.Parallel()
	fs, err := newTestUnixFS()
	if err != nil {
		t.Fatal(err)
		return
	}
	defer fs.Cleanup()

	// TODO: implement
}

func TestUnixFS_Chown(t *testing.T) {
	t.Parallel()
	fs, err := newTestUnixFS()
	if err != nil {
		t.Fatal(err)
		return
	}
	defer fs.Cleanup()

	// TODO: implement
}

func TestUnixFS_Lchown(t *testing.T) {
	t.Parallel()
	fs, err := newTestUnixFS()
	if err != nil {
		t.Fatal(err)
		return
	}
	defer fs.Cleanup()

	// TODO: implement
}

func TestUnixFS_Chtimes(t *testing.T) {
	t.Parallel()
	fs, err := newTestUnixFS()
	if err != nil {
		t.Fatal(err)
		return
	}
	defer fs.Cleanup()

	// TODO: implement
}

func TestUnixFS_Create(t *testing.T) {
	t.Parallel()
	fs, err := newTestUnixFS()
	if err != nil {
		t.Fatal(err)
		return
	}
	defer fs.Cleanup()

	// TODO: implement
}

func TestUnixFS_Mkdir(t *testing.T) {
	t.Parallel()
	fs, err := newTestUnixFS()
	if err != nil {
		t.Fatal(err)
		return
	}
	defer fs.Cleanup()

	// TODO: implement
}

func TestUnixFS_MkdirAll(t *testing.T) {
	t.Parallel()
	fs, err := newTestUnixFS()
	if err != nil {
		t.Fatal(err)
		return
	}
	defer fs.Cleanup()

	if err := fs.MkdirAll("/a/bunch/of/directories", 0o755); err != nil {
		t.Error(err)
		return
	}

	// TODO: stat sanity check
}

func TestUnixFS_Open(t *testing.T) {
	t.Parallel()
	fs, err := newTestUnixFS()
	if err != nil {
		t.Fatal(err)
		return
	}
	defer fs.Cleanup()

	// TODO: implement
}

func TestUnixFS_OpenFile(t *testing.T) {
	t.Parallel()
	fs, err := newTestUnixFS()
	if err != nil {
		t.Fatal(err)
		return
	}
	defer fs.Cleanup()

	// TODO: implement
}

func TestUnixFS_ReadDir(t *testing.T) {
	t.Parallel()
	fs, err := newTestUnixFS()
	if err != nil {
		t.Fatal(err)
		return
	}
	defer fs.Cleanup()

	// TODO: implement
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
		f, err := fs.Create("test_file")
		if err != nil {
			t.Error(err)
			return
		}
		_ = f.Close()

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

func TestUnixFS_Stat(t *testing.T) {
	t.Parallel()
	fs, err := newTestUnixFS()
	if err != nil {
		t.Fatal(err)
		return
	}
	defer fs.Cleanup()

	// TODO: implement
}

func TestUnixFS_Lstat(t *testing.T) {
	t.Parallel()
	fs, err := newTestUnixFS()
	if err != nil {
		t.Fatal(err)
		return
	}
	defer fs.Cleanup()

	// TODO: implement
}

func TestUnixFS_Symlink(t *testing.T) {
	t.Parallel()
	fs, err := newTestUnixFS()
	if err != nil {
		t.Fatal(err)
		return
	}
	defer fs.Cleanup()

	// TODO: implement
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

func TestUnixFS_WalkDir(t *testing.T) {
	t.Parallel()
	fs, err := newTestUnixFS()
	if err != nil {
		t.Fatal(err)
		return
	}
	defer fs.Cleanup()

	//for i := 0; i < 5; i++ {
	//	dirName := "dir" + strconv.Itoa(i)
	//	if err := fs.Mkdir(dirName, 0o755); err != nil {
	//		t.Error(err)
	//		return
	//	}
	//	for j := 0; j < 5; j++ {
	//		f, err := fs.Create(filepath.Join(dirName, "file"+strconv.Itoa(j)))
	//		if err != nil {
	//			t.Error(err)
	//			return
	//		}
	//		_ = f.Close()
	//	}
	//}
	//
	//if err := fs.WalkDir(".", func(path string, info ufs.DirEntry, err error) error {
	//	if err != nil {
	//		return err
	//	}
	//	t.Log(path)
	//	return nil
	//}); err != nil {
	//	t.Error(err)
	//	return
	//}
}

func TestUnixFS_WalkDirat(t *testing.T) {
	t.Parallel()
	fs, err := newTestUnixFS()
	if err != nil {
		t.Fatal(err)
		return
	}
	defer fs.Cleanup()

	for i := 0; i < 2; i++ {
		dirName := "base" + strconv.Itoa(i)
		if err := fs.Mkdir(dirName, 0o755); err != nil {
			t.Error(err)
			return
		}
		for j := 0; j < 1; j++ {
			f, err := fs.Create(filepath.Join(dirName, "file"+strconv.Itoa(j)))
			if err != nil {
				t.Error(err)
				return
			}
			_ = f.Close()
			if err := fs.Mkdir(filepath.Join(dirName, "dir"+strconv.Itoa(j)), 0o755); err != nil {
				t.Error(err)
				return
			}
			f, err = fs.Create(filepath.Join(dirName, "dir"+strconv.Itoa(j), "file"+strconv.Itoa(j)))
			if err != nil {
				t.Error(err)
				return
			}
			_ = f.Close()
		}
	}

	t.Run("walk starting at the filesystem root", func(t *testing.T) {
		pathsTraversed, err := fs.testWalkDirAt("")
		if err != nil {
			t.Error(err)
			return
		}
		expect := []Path{
			{Name: ".", Relative: "."},
			{Name: "base0", Relative: "base0"},
			{Name: "dir0", Relative: "base0/dir0"},
			{Name: "file0", Relative: "base0/dir0/file0"},
			{Name: "file0", Relative: "base0/file0"},
			{Name: "base1", Relative: "base1"},
			{Name: "dir0", Relative: "base1/dir0"},
			{Name: "file0", Relative: "base1/dir0/file0"},
			{Name: "file0", Relative: "base1/file0"},
		}
		if !reflect.DeepEqual(pathsTraversed, expect) {
			t.Log(pathsTraversed)
			t.Log(expect)
			t.Error("walk doesn't match")
			return
		}
	})

	t.Run("walk starting in a directory", func(t *testing.T) {
		pathsTraversed, err := fs.testWalkDirAt("base0")
		if err != nil {
			t.Error(err)
			return
		}
		expect := []Path{
			// TODO: what should relative actually be here?
			// The behaviour differs from walking the directory root vs a sub
			// directory. When walking from the root, dirfd is the directory we
			// are walking from and both name and relative are `.`. However,
			// when walking from a subdirectory, fd is the parent of the
			// subdirectory, and name is the subdirectory.
			{Name: "base0", Relative: "."},
			{Name: "dir0", Relative: "dir0"},
			{Name: "file0", Relative: "dir0/file0"},
			{Name: "file0", Relative: "file0"},
		}
		if !reflect.DeepEqual(pathsTraversed, expect) {
			t.Log(pathsTraversed)
			t.Log(expect)
			t.Error("walk doesn't match")
			return
		}
	})
}

type Path struct {
	Name     string
	Relative string
}

func (fs *testUnixFS) testWalkDirAt(path string) ([]Path, error) {
	dirfd, name, closeFd, err := fs.SafePath(path)
	defer closeFd()
	if err != nil {
		return nil, err
	}
	var pathsTraversed []Path
	if err := fs.WalkDirat(dirfd, name, func(_ int, name, relative string, _ ufs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		pathsTraversed = append(pathsTraversed, Path{Name: name, Relative: relative})
		return nil
	}); err != nil {
		return nil, err
	}
	slices.SortStableFunc(pathsTraversed, func(a, b Path) int {
		if a.Relative > b.Relative {
			return 1
		}
		if a.Relative < b.Relative {
			return -1
		}
		return 0
	})
	return pathsTraversed, nil
}
