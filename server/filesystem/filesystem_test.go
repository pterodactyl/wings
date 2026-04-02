package filesystem

import (
	"bufio"
	"bytes"
	"errors"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"unicode/utf8"

	. "github.com/franela/goblin"

	"github.com/pterodactyl/wings/config"
)

type testFs struct {
	*Filesystem
	tmpDir string
}

func NewFs() *testFs {
	config.Set(&config.Configuration{
		AuthenticationToken: "abc",
		System: config.SystemConfiguration{
			RootDirectory:     "/server",
			DiskCheckInterval: 150,
		},
	})

	tmpDir, err := os.MkdirTemp(os.TempDir(), "pterodactyl")
	if err != nil {
		panic(err)
	}

	fs, err := New(tmpDir, 0, []string{})
	if err != nil {
		panic(err)
	}
	fs.isTest = true

	tfs := &testFs{Filesystem: fs, tmpDir: tmpDir}
	tfs.reset()

	return tfs
}

func getFileContent(file *os.File) string {
	var w bytes.Buffer
	if _, err := bufio.NewReader(file).WriteTo(&w); err != nil {
		panic(err)
	}
	return w.String()
}

func (tfs *testFs) reset() {
	if err := tfs.root.Close(); err != nil {
		panic(err)
	}
	if !strings.HasPrefix(tfs.tmpDir, "/tmp/pterodactyl") {
		panic("filesystem_test: attempting to delete outside root directory: " + tfs.tmpDir)
	}

	if err := os.RemoveAll(tfs.tmpDir); err != nil {
		if !os.IsNotExist(err) {
			panic(err)
		}
	}

	if !strings.HasPrefix(tfs.rootPath, tfs.tmpDir) {
		panic("filesystem_test: mismatch between root and tmp paths")
	}

	tfs.rootPath = filepath.Join(tfs.tmpDir, "/server")
	if err := os.MkdirAll(tfs.rootPath, 0o755); err != nil {
		panic(err)
	}

	r, err := os.OpenRoot(tfs.rootPath)
	if err != nil {
		panic(err)
	}

	tfs.root = r
}

func (tfs *testFs) write(name string, data []byte) {
	p := filepath.Clean(filepath.Join(tfs.rootPath, name))
	// Ensure we're always writing into a location that would also be cleaned up
	// by the reset() function.
	if !strings.HasPrefix(p, filepath.Dir(tfs.rootPath)) {
		panic("filesystem_test: attempting to write outside of root directory: " + p)
	}

	if err := os.WriteFile(filepath.Join(tfs.rootPath, name), data, 0o644); err != nil {
		panic(err)
	}
}

func TestFilesystem_Openfile(t *testing.T) {
	g := Goblin(t)
	fs := NewFs()

	g.Describe("File", func() {
		g.It("returns custom error when file does not exist", func() {
			_, _, err := fs.File("foo/bar.txt")

			g.Assert(err).IsNotNil()
			g.Assert(IsErrorCode(err, ErrNotExist)).IsTrue()
		})

		g.It("returns file stat information", func() {
			fs.write("foo.txt", []byte("hello world"))

			f, st, err := fs.File("foo.txt")
			g.Assert(err).IsNil()

			g.Assert(st.Name()).Equal("foo.txt")
			g.Assert(f).IsNotNil()
			_ = f.Close()
		})

		g.AfterEach(func() {
			fs.reset()
		})
	})
}

func TestFilesystem_Writefile(t *testing.T) {
	g := Goblin(t)
	fs := NewFs()

	g.Describe("Open and WriteFile", func() {
		buf := &bytes.Buffer{}

		// Test that a file can be written to the disk and that the disk space used as a result
		// is updated correctly in the end.
		g.It("can create a new file", func() {
			r := bytes.NewReader([]byte("test file content"))

			g.Assert(atomic.LoadInt64(&fs.diskUsed)).Equal(int64(0))

			err := fs.Writefile("test.txt", r)
			g.Assert(err).IsNil()

			f, _, err := fs.File("test.txt")
			g.Assert(err).IsNil()
			defer f.Close()
			g.Assert(getFileContent(f)).Equal("test file content")
			g.Assert(atomic.LoadInt64(&fs.diskUsed)).Equal(r.Size())
		})

		g.It("can create a new file inside a nested directory with leading slash", func() {
			r := bytes.NewReader([]byte("test file content"))

			err := fs.Writefile("/some/nested/test.txt", r)
			g.Assert(err).IsNil()

			f, _, err := fs.File("/some/nested/test.txt")
			g.Assert(err).IsNil()
			defer f.Close()
			g.Assert(getFileContent(f)).Equal("test file content")
		})

		g.It("can create a new file inside a nested directory without a trailing slash", func() {
			r := bytes.NewReader([]byte("test file content"))

			err := fs.Writefile("some/../foo/bar/test.txt", r)
			g.Assert(err).IsNil()

			f, _, err := fs.File("foo/bar/test.txt")
			g.Assert(err).IsNil()
			defer f.Close()
			g.Assert(getFileContent(f)).Equal("test file content")
		})

		g.It("cannot create a file outside the root directory", func() {
			r := bytes.NewReader([]byte("test file content"))

			err := fs.Writefile("../../etc/test.txt", r)
			g.Assert(err).IsNotNil()
			g.Assert(IsPathError(err)).IsTrue()

			err = fs.Writefile("a/../../../test.txt", r)
			g.Assert(err).IsNotNil()
			g.Assert(IsPathError(err)).IsTrue()
		})

		g.It("cannot write a file that exceeds the disk limits", func() {
			atomic.StoreInt64(&fs.diskLimit, 1024)

			b := make([]byte, 1025)
			_, err := rand.Read(b)
			g.Assert(err).IsNil()
			g.Assert(len(b)).Equal(1025)

			r := bytes.NewReader(b)
			err = fs.Writefile("test.txt", r)
			g.Assert(err).IsNotNil()
			g.Assert(IsErrorCode(err, ErrCodeDiskSpace)).IsTrue()
		})

		g.It("truncates the file when writing new contents", func() {
			r := bytes.NewReader([]byte("original data"))
			err := fs.Writefile("test.txt", r)
			g.Assert(err).IsNil()

			r = bytes.NewReader([]byte("new data"))
			err = fs.Writefile("test.txt", r)
			g.Assert(err).IsNil()

			f, _, err := fs.File("test.txt")
			g.Assert(err).IsNil()
			defer f.Close()
			g.Assert(getFileContent(f)).Equal("new data")
		})

		g.AfterEach(func() {
			buf.Truncate(0)
			fs.reset()

			atomic.StoreInt64(&fs.diskUsed, 0)
			atomic.StoreInt64(&fs.diskLimit, 0)
		})
	})
}

func TestFilesystem_CreateDirectory(t *testing.T) {
	g := Goblin(t)
	fs := NewFs()

	g.Describe("CreateDirectory", func() {
		g.It("should create missing directories automatically", func() {
			err := fs.CreateDirectory("test", "foo/bar/baz")
			g.Assert(err).IsNil()

			st, err := os.Stat(filepath.Join(fs.rootPath, "foo/bar/baz/test"))
			g.Assert(err).IsNil()
			g.Assert(st.IsDir()).IsTrue()
			g.Assert(st.Name()).Equal("test")
		})

		g.It("should work with leading and trailing slashes", func() {
			err := fs.CreateDirectory("test", "/foozie/barzie/bazzy/")
			g.Assert(err).IsNil()

			st, err := os.Stat(filepath.Join(fs.rootPath, "foozie/barzie/bazzy/test"))
			g.Assert(err).IsNil()
			g.Assert(st.IsDir()).IsTrue()
			g.Assert(st.Name()).Equal("test")
		})

		g.It("should not allow the creation of directories outside the root", func() {
			err := fs.CreateDirectory("test", "e/../../something")
			g.Assert(err).IsNotNil()
			g.Assert(IsPathError(err)).IsTrue()
		})

		g.It("should not increment the disk usage", func() {
			err := fs.CreateDirectory("test", "/")
			g.Assert(err).IsNil()
			g.Assert(atomic.LoadInt64(&fs.diskUsed)).Equal(int64(0))
		})

		g.AfterEach(func() {
			fs.reset()
		})
	})
}

func TestFilesystem_Rename(t *testing.T) {
	g := Goblin(t)
	fs := NewFs()

	g.Describe("Rename", func() {
		g.BeforeEach(func() {
			fs.write("source.txt", []byte("text content"))
		})

		g.It("returns an error if the target already exists", func() {
			fs.write("target.txt", []byte("taget content"))

			err := fs.Rename("source.txt", "target.txt")
			g.Assert(err).IsNotNil()
			g.Assert(errors.Is(err, os.ErrExist)).IsTrue()
		})

		g.It("returns an error if the final destination is the root directory", func() {
			err := fs.Rename("source.txt", "/")
			g.Assert(err).IsNotNil()
			g.Assert(errors.Is(err, os.ErrExist)).IsTrue()
		})

		g.It("returns an error if the source destination is the root directory", func() {
			err := fs.Rename("/", "destination.txt")
			g.Assert(err).IsNotNil()
			g.Assert(errors.Is(err, os.ErrExist)).IsTrue()
		})

		g.It("does not allow renaming to a location outside the root", func() {
			err := fs.Rename("source.txt", "../target.txt")
			g.Assert(err).IsNotNil()
			g.Assert(IsPathError(err)).IsTrue()
		})

		g.It("does not allow renaming from a location outside the root", func() {
			err := fs.Rename("../ext-source.txt", "target.txt")
			g.Assert(err).IsNotNil()
			g.Assert(IsLinkError(err)).IsTrue()
		})

		g.It("allows a file to be renamed", func() {
			err := fs.Rename("source.txt", "target.txt")
			g.Assert(err).IsNil()

			_, err = os.Stat(filepath.Join(fs.rootPath, "source.txt"))
			g.Assert(err).IsNotNil()
			g.Assert(errors.Is(err, os.ErrNotExist)).IsTrue()

			st, err := os.Stat(filepath.Join(fs.rootPath, "target.txt"))
			g.Assert(err).IsNil()
			g.Assert(st.Name()).Equal("target.txt")
			g.Assert(st.Size()).IsNotZero()
		})

		g.It("allows a folder to be renamed", func() {
			if err := os.Mkdir(filepath.Join(fs.rootPath, "/source_dir"), 0o755); err != nil {
				panic(err)
			}

			err := fs.Rename("source_dir", "target_dir")
			g.Assert(err).IsNil()

			_, err = os.Stat(filepath.Join(fs.rootPath, "source_dir"))
			g.Assert(err).IsNotNil()
			g.Assert(errors.Is(err, os.ErrNotExist)).IsTrue()

			st, err := os.Stat(filepath.Join(fs.rootPath, "target_dir"))
			g.Assert(err).IsNil()
			g.Assert(st.IsDir()).IsTrue()
		})

		g.It("returns an error if the source does not exist", func() {
			err := fs.Rename("missing.txt", "target.txt")
			g.Assert(err).IsNotNil()
			g.Assert(errors.Is(err, os.ErrNotExist)).IsTrue()
		})

		g.It("creates directories if they are missing", func() {
			err := fs.Rename("source.txt", "nested/folder/target.txt")
			g.Assert(err).IsNil()

			st, err := os.Stat(filepath.Join(fs.rootPath, "nested/folder/target.txt"))
			g.Assert(err).IsNil()
			g.Assert(st.Name()).Equal("target.txt")
		})

		g.AfterEach(func() {
			fs.reset()
		})
	})
}

func TestFilesystem_Copy(t *testing.T) {
	g := Goblin(t)
	fs := NewFs()

	g.Describe("Copy", func() {
		g.BeforeEach(func() {
			fs.write("source.txt", []byte("text content"))
			atomic.StoreInt64(&fs.diskUsed, int64(utf8.RuneCountInString("test content")))
		})

		g.It("should return an error if the source does not exist", func() {
			err := fs.Copy("foo.txt")
			g.Assert(err).IsNotNil()
			g.Assert(errors.Is(err, os.ErrNotExist)).IsTrue()
		})

		g.It("should return an error if the source is outside the root", func() {
			err := fs.Copy("../ext-source.txt")
			g.Assert(err).IsNotNil()
			g.Assert(IsPathError(err)).IsTrue()
		})

		g.It("should return an error if the source directory is outside the root", func() {
			err := fs.Copy("../nested/in/dir/ext-source.txt")
			g.Assert(err).IsNotNil()
			g.Assert(IsPathError(err)).IsTrue()

			err = fs.Copy("nested/in/../../../nested/in/dir/ext-source.txt")
			g.Assert(err).IsNotNil()
			g.Assert(IsPathError(err)).IsTrue()
		})

		g.It("should return an error if the source is a directory", func() {
			err := os.Mkdir(filepath.Join(fs.rootPath, "/dir"), 0o755)
			g.Assert(err).IsNil()

			err = fs.Copy("dir")
			g.Assert(err).IsNotNil()
			g.Assert(errors.Is(err, os.ErrNotExist)).IsTrue()
		})

		g.It("should return an error if there is not space to copy the file", func() {
			atomic.StoreInt64(&fs.diskLimit, 2)

			err := fs.Copy("source.txt")
			g.Assert(err).IsNotNil()
			g.Assert(IsErrorCode(err, ErrCodeDiskSpace)).IsTrue()
		})

		g.It("should create a copy of the file and increment the disk used", func() {
			err := fs.Copy("source.txt")
			g.Assert(err).IsNil()

			_, err = os.Stat(filepath.Join(fs.rootPath, "source.txt"))
			g.Assert(err).IsNil()

			_, err = os.Stat(filepath.Join(fs.rootPath, "source copy.txt"))
			g.Assert(err).IsNil()
		})

		g.It("should create a copy of the file with a suffix if a copy already exists", func() {
			err := fs.Copy("source.txt")
			g.Assert(err).IsNil()

			err = fs.Copy("source.txt")
			g.Assert(err).IsNil()

			r := []string{"source.txt", "source copy.txt", "source copy 1.txt"}

			for _, name := range r {
				_, err = os.Stat(filepath.Join(fs.rootPath, name))
				g.Assert(err).IsNil()
			}

			g.Assert(atomic.LoadInt64(&fs.diskUsed)).Equal(int64(utf8.RuneCountInString("test content")) * 3)
		})

		g.It("should create a copy inside of a directory", func() {
			if err := os.MkdirAll(filepath.Join(fs.rootPath, "/nested/in/dir"), 0o755); err != nil {
				panic(err)
			}

			fs.write("nested/in/dir/source.txt", []byte("test content"))

			err := fs.Copy("nested/in/dir/source.txt")
			g.Assert(err).IsNil()

			_, err = os.Stat(filepath.Join(fs.rootPath, "nested/in/dir/source.txt"))
			g.Assert(err).IsNil()

			_, err = os.Stat(filepath.Join(fs.rootPath, "nested/in/dir/source copy.txt"))
			g.Assert(err).IsNil()
		})

		g.AfterEach(func() {
			fs.reset()

			atomic.StoreInt64(&fs.diskUsed, 0)
			atomic.StoreInt64(&fs.diskLimit, 0)
		})
	})
}

func TestFilesystem_Symlink(t *testing.T) {
	g := Goblin(t)
	fs := NewFs()

	g.Describe("Symlink", func() {
		g.It("should create a symlink", func() {
			fs.write("source.txt", []byte("text content"))

			err := fs.Symlink("source.txt", "symlink.txt")
			g.Assert(err).IsNil()

			st, err := os.Lstat(filepath.Join(fs.rootPath, "symlink.txt"))
			g.Assert(err).IsNil()
			g.Assert(st.Mode()&os.ModeSymlink != 0).IsTrue()
		})

		g.It("should return an error if the source is outside the root", func() {
			err := fs.Symlink("../source.txt", "symlink.txt")
			g.Assert(err).IsNotNil()
			g.Assert(IsPathError(err)).IsTrue()
		})

		g.It("should return an error if the dest is outside the root", func() {
			fs.write("source.txt", []byte("text content"))

			err := fs.Symlink("source.txt", "../symlink.txt")
			g.Assert(err).IsNotNil()
			g.Assert(IsLinkError(err)).IsTrue()
		})

		g.AfterEach(func() {
			fs.reset()
		})
	})
}

func TestFilesystem_ReadDir(t *testing.T) {
	g := Goblin(t)
	fs := NewFs()

	g.Describe("ReadDir", func() {
		g.Before(func() {
			if err := os.Mkdir(filepath.Join(fs.rootPath, "child"), 0o755); err != nil {
				panic(err)
			}

			fs.write("one.txt", []byte("one"))
			fs.write("two.txt", []byte("two"))
			fs.write("child/three.txt", []byte("two"))
		})

		g.After(func() {
			fs.reset()
		})

		g.It("should return the contents of the root directory", func() {
			d, err := fs.ReadDir("/")
			g.Assert(err).IsNil()
			g.Assert(len(d)).Equal(3)

			// os.Root#ReadDir sorts them by name.
			g.Assert(d[0].Name()).Equal("child")
			g.Assert(d[0].IsDir()).IsTrue()
			g.Assert(d[1].Name()).Equal("one.txt")
			g.Assert(d[2].Name()).Equal("two.txt")
		})

		g.It("should return the contents of a child directory", func() {
			d, err := fs.ReadDir("child")
			g.Assert(err).IsNil()
			g.Assert(len(d)).Equal(1)
			g.Assert(d[0].Name()).Equal("three.txt")
		})

		g.It("should return an error if the directory is outside the root", func() {
			_, err := fs.ReadDir("../server")
			g.Assert(err).IsNotNil()
			g.Assert(IsPathError(err)).IsTrue()
		})
	})
}

func TestFilesystem_Delete(t *testing.T) {
	g := Goblin(t)
	fs := NewFs()

	g.Describe("Delete", func() {
		g.BeforeEach(func() {
			fs.write("source.txt", []byte("text content"))
			atomic.StoreInt64(&fs.diskUsed, int64(utf8.RuneCountInString("test content")))
		})

		g.It("does not delete files outside the root directory", func() {
			err := fs.Delete("../ext-source.txt")
			g.Assert(err).IsNotNil()
			g.Assert(IsPathError(err)).IsTrue()
		})

		g.It("does not allow the deletion of the root directory", func() {
			err := fs.Delete("/")
			g.Assert(err).IsNotNil()
			g.Assert(err.Error()).Equal("server/filesystem: delete: cannot delete root directory")
		})

		g.It("does not return an error if the target does not exist", func() {
			err := fs.Delete("missing.txt")
			g.Assert(err).IsNil()

			st, err := os.Stat(filepath.Join(fs.rootPath, "source.txt"))
			g.Assert(err).IsNil()
			g.Assert(st.Name()).Equal("source.txt")
		})

		g.It("deletes files and subtracts their size from the disk usage", func() {
			err := fs.Delete("source.txt")
			g.Assert(err).IsNil()

			_, err = os.Stat(filepath.Join(fs.rootPath, "source.txt"))
			g.Assert(err).IsNotNil()
			g.Assert(errors.Is(err, os.ErrNotExist)).IsTrue()

			g.Assert(atomic.LoadInt64(&fs.diskUsed)).Equal(int64(0))
		})

		g.It("deletes all items inside a directory if the directory is deleted", func() {
			sources := []string{
				"foo/source.txt",
				"foo/bar/source.txt",
				"foo/bar/baz/source.txt",
			}

			if err := os.MkdirAll(filepath.Join(fs.rootPath, "/foo/bar/baz"), 0o755); err != nil {
				panic(err)
			}

			for _, s := range sources {
				fs.write(s, []byte("test content"))
			}

			atomic.StoreInt64(&fs.diskUsed, int64(utf8.RuneCountInString("test content")*3))

			err := fs.Delete("foo")
			g.Assert(err).IsNil()
			g.Assert(atomic.LoadInt64(&fs.diskUsed)).Equal(int64(0))

			for _, s := range sources {
				_, err = os.Stat(filepath.Join(fs.rootPath, s))
				g.Assert(err).IsNotNil()
				g.Assert(errors.Is(err, os.ErrNotExist)).IsTrue()
			}
		})

		g.It("deletes a symlink but not it's target within the root directory", func() {
			// Symlink to a file inside the root server data directory.
			if err := os.Symlink(filepath.Join(fs.rootPath, "source.txt"), filepath.Join(fs.rootPath, "symlink.txt")); err != nil {
				panic(err)
			}

			// Delete the symlink itself.
			err := fs.Delete("symlink.txt")
			g.Assert(err).IsNil()

			// Ensure the symlink was deleted.
			_, err = os.Lstat(filepath.Join(fs.rootPath, "symlink.txt"))
			g.Assert(err).IsNotNil()

			// Ensure the symlink target still exists.
			_, err = os.Lstat(filepath.Join(fs.rootPath, "source.txt"))
			g.Assert(err).IsNil()
		})

		g.It("does not delete files symlinked outside of the root directory", func() {
			// Create a file outside the root directory.
			fs.write("../external.txt", []byte("test content"))

			// Create a symlink to the file outside the root directory.
			if err := os.Symlink(filepath.Join(fs.rootPath, "../external.txt"), filepath.Join(fs.rootPath, "symlink.txt")); err != nil {
				panic(err)
			}

			// Delete the symlink. (This should pass as we will delete the symlink itself, not the target)
			err := fs.Delete("symlink.txt")
			g.Assert(err).IsNil()

			// Ensure the file outside the root directory still exists.
			_, err = os.Lstat(filepath.Join(fs.rootPath, "../external.txt"))
			g.Assert(err).IsNil()
		})

		g.It("does not delete files symlinked through a directory outside of the root directory", func() {
			// Create a directory outside the root directory.
			if err := os.Mkdir(filepath.Join(fs.rootPath, "../external"), 0o755); err != nil {
				panic(err)
			}

			fs.write("../external/source.txt", []byte("test content"))

			// Symlink the directory that is outside the root to a file inside the root.
			if err := os.Symlink(filepath.Join(fs.rootPath, "../external"), filepath.Join(fs.rootPath, "/symlink")); err != nil {
				panic(err)
			}

			// Delete a file inside the symlinked directory.
			err := fs.Delete("symlink/source.txt")
			g.Assert(err).IsNotNil()
			g.Assert(IsPathError(err)).IsTrue()

			// Ensure the file outside the root directory still exists.
			_, err = os.Lstat(filepath.Join(fs.rootPath, "../external/source.txt"))
			g.Assert(err).IsNil()
		})

		g.It("returns an error when trying to delete a non-existent file symlinked through a directory outside of the root directory", func() {
			// Create a directory outside the root directory.
			if err := os.Mkdir(filepath.Join(fs.rootPath, "../external"), 0o755); err != nil {
				panic(err)
			}

			// Symlink the directory that is outside the root to a file inside the root.
			if err := os.Symlink(filepath.Join(fs.rootPath, "../external"), filepath.Join(fs.rootPath, "/symlink")); err != nil {
				panic(err)
			}

			// Delete a file inside the symlinked directory.
			err := fs.Delete("symlink/source.txt")
			g.Assert(err).IsNotNil()
			g.Assert(IsPathError(err)).IsTrue()
		})

		g.AfterEach(func() {
			fs.reset()

			atomic.StoreInt64(&fs.diskUsed, 0)
			atomic.StoreInt64(&fs.diskLimit, 0)
		})
	})
}
