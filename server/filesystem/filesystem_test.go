package filesystem

import (
	"bufio"
	"bytes"
	"errors"
	"math/rand"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"unicode/utf8"

	. "github.com/franela/goblin"

	"github.com/pterodactyl/wings/config"
)

func NewFs() (*Filesystem, *rootFs) {
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
	// defer os.RemoveAll(tmpDir)

	rfs := rootFs{root: tmpDir}

	rfs.reset()

	fs := New(filepath.Join(tmpDir, "/server"), 0, []string{})
	fs.isTest = true

	return fs, &rfs
}

type rootFs struct {
	root string
}

func getFileContent(file *os.File) string {
	var w bytes.Buffer
	if _, err := bufio.NewReader(file).WriteTo(&w); err != nil {
		panic(err)
	}
	return w.String()
}

func (rfs *rootFs) CreateServerFile(p string, c []byte) error {
	f, err := os.Create(filepath.Join(rfs.root, "/server", p))

	if err == nil {
		f.Write(c)
		f.Close()
	}

	return err
}

func (rfs *rootFs) CreateServerFileFromString(p string, c string) error {
	return rfs.CreateServerFile(p, []byte(c))
}

func (rfs *rootFs) StatServerFile(p string) (os.FileInfo, error) {
	return os.Stat(filepath.Join(rfs.root, "/server", p))
}

func (rfs *rootFs) reset() {
	if err := os.RemoveAll(filepath.Join(rfs.root, "/server")); err != nil {
		if !os.IsNotExist(err) {
			panic(err)
		}
	}

	if err := os.Mkdir(filepath.Join(rfs.root, "/server"), 0o755); err != nil {
		panic(err)
	}
}

func TestFilesystem_Openfile(t *testing.T) {
	g := Goblin(t)
	fs, rfs := NewFs()

	g.Describe("File", func() {
		g.It("returns custom error when file does not exist", func() {
			_, _, err := fs.File("foo/bar.txt")

			g.Assert(err).IsNotNil()
			g.Assert(IsErrorCode(err, ErrNotExist)).IsTrue()
		})

		g.It("returns file stat information", func() {
			_ = rfs.CreateServerFile("foo.txt", []byte("hello world"))

			f, st, err := fs.File("foo.txt")
			g.Assert(err).IsNil()

			g.Assert(st.Name()).Equal("foo.txt")
			g.Assert(f).IsNotNil()
			_ = f.Close()
		})

		g.AfterEach(func() {
			rfs.reset()
		})
	})
}

func TestFilesystem_Writefile(t *testing.T) {
	g := Goblin(t)
	fs, rfs := NewFs()

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

			err := fs.Writefile("/some/../foo/../../test.txt", r)
			g.Assert(err).IsNotNil()
			g.Assert(IsErrorCode(err, ErrCodePathResolution)).IsTrue()
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
			rfs.reset()

			atomic.StoreInt64(&fs.diskUsed, 0)
			atomic.StoreInt64(&fs.diskLimit, 0)
		})
	})
}

func TestFilesystem_CreateDirectory(t *testing.T) {
	g := Goblin(t)
	fs, rfs := NewFs()

	g.Describe("CreateDirectory", func() {
		g.It("should create missing directories automatically", func() {
			err := fs.CreateDirectory("test", "foo/bar/baz")
			g.Assert(err).IsNil()

			st, err := rfs.StatServerFile("foo/bar/baz/test")
			g.Assert(err).IsNil()
			g.Assert(st.IsDir()).IsTrue()
			g.Assert(st.Name()).Equal("test")
		})

		g.It("should work with leading and trailing slashes", func() {
			err := fs.CreateDirectory("test", "/foozie/barzie/bazzy/")
			g.Assert(err).IsNil()

			st, err := rfs.StatServerFile("foozie/barzie/bazzy/test")
			g.Assert(err).IsNil()
			g.Assert(st.IsDir()).IsTrue()
			g.Assert(st.Name()).Equal("test")
		})

		g.It("should not allow the creation of directories outside the root", func() {
			err := fs.CreateDirectory("test", "e/../../something")
			g.Assert(err).IsNotNil()
			g.Assert(IsErrorCode(err, ErrCodePathResolution)).IsTrue()
		})

		g.It("should not increment the disk usage", func() {
			err := fs.CreateDirectory("test", "/")
			g.Assert(err).IsNil()
			g.Assert(atomic.LoadInt64(&fs.diskUsed)).Equal(int64(0))
		})

		g.AfterEach(func() {
			rfs.reset()
		})
	})
}

func TestFilesystem_Rename(t *testing.T) {
	g := Goblin(t)
	fs, rfs := NewFs()

	g.Describe("Rename", func() {
		g.BeforeEach(func() {
			if err := rfs.CreateServerFileFromString("source.txt", "text content"); err != nil {
				panic(err)
			}
		})

		g.It("returns an error if the target already exists", func() {
			err := rfs.CreateServerFileFromString("target.txt", "taget content")
			g.Assert(err).IsNil()

			err = fs.Rename("source.txt", "target.txt")
			g.Assert(err).IsNotNil()
			g.Assert(errors.Is(err, os.ErrExist)).IsTrue()
		})

		g.It("returns an error if the final destination is the root directory", func() {
			err := fs.Rename("source.txt", "/")
			g.Assert(err).IsNotNil()
			g.Assert(errors.Is(err, os.ErrExist)).IsTrue()
		})

		g.It("returns an error if the source destination is the root directory", func() {
			err := fs.Rename("source.txt", "/")
			g.Assert(err).IsNotNil()
			g.Assert(errors.Is(err, os.ErrExist)).IsTrue()
		})

		g.It("does not allow renaming to a location outside the root", func() {
			err := fs.Rename("source.txt", "../target.txt")
			g.Assert(err).IsNotNil()
			g.Assert(IsErrorCode(err, ErrCodePathResolution)).IsTrue()
		})

		g.It("does not allow renaming from a location outside the root", func() {
			err := rfs.CreateServerFileFromString("/../ext-source.txt", "taget content")

			err = fs.Rename("/../ext-source.txt", "target.txt")
			g.Assert(err).IsNotNil()
			g.Assert(IsErrorCode(err, ErrCodePathResolution)).IsTrue()
		})

		g.It("allows a file to be renamed", func() {
			err := fs.Rename("source.txt", "target.txt")
			g.Assert(err).IsNil()

			_, err = rfs.StatServerFile("source.txt")
			g.Assert(err).IsNotNil()
			g.Assert(errors.Is(err, os.ErrNotExist)).IsTrue()

			st, err := rfs.StatServerFile("target.txt")
			g.Assert(err).IsNil()
			g.Assert(st.Name()).Equal("target.txt")
			g.Assert(st.Size()).IsNotZero()
		})

		g.It("allows a folder to be renamed", func() {
			err := os.Mkdir(filepath.Join(rfs.root, "/server/source_dir"), 0o755)
			g.Assert(err).IsNil()

			err = fs.Rename("source_dir", "target_dir")
			g.Assert(err).IsNil()

			_, err = rfs.StatServerFile("source_dir")
			g.Assert(err).IsNotNil()
			g.Assert(errors.Is(err, os.ErrNotExist)).IsTrue()

			st, err := rfs.StatServerFile("target_dir")
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

			st, err := rfs.StatServerFile("nested/folder/target.txt")
			g.Assert(err).IsNil()
			g.Assert(st.Name()).Equal("target.txt")
		})

		g.AfterEach(func() {
			rfs.reset()
		})
	})
}

func TestFilesystem_Copy(t *testing.T) {
	g := Goblin(t)
	fs, rfs := NewFs()

	g.Describe("Copy", func() {
		g.BeforeEach(func() {
			if err := rfs.CreateServerFileFromString("source.txt", "text content"); err != nil {
				panic(err)
			}

			atomic.StoreInt64(&fs.diskUsed, int64(utf8.RuneCountInString("test content")))
		})

		g.It("should return an error if the source does not exist", func() {
			err := fs.Copy("foo.txt")
			g.Assert(err).IsNotNil()
			g.Assert(errors.Is(err, os.ErrNotExist)).IsTrue()
		})

		g.It("should return an error if the source is outside the root", func() {
			err := rfs.CreateServerFileFromString("/../ext-source.txt", "text content")

			err = fs.Copy("../ext-source.txt")
			g.Assert(err).IsNotNil()
			g.Assert(IsErrorCode(err, ErrCodePathResolution)).IsTrue()
		})

		g.It("should return an error if the source directory is outside the root", func() {
			err := os.MkdirAll(filepath.Join(rfs.root, "/nested/in/dir"), 0o755)
			g.Assert(err).IsNil()

			err = rfs.CreateServerFileFromString("/../nested/in/dir/ext-source.txt", "external content")
			g.Assert(err).IsNil()

			err = fs.Copy("../nested/in/dir/ext-source.txt")
			g.Assert(err).IsNotNil()
			g.Assert(IsErrorCode(err, ErrCodePathResolution)).IsTrue()

			err = fs.Copy("nested/in/../../../nested/in/dir/ext-source.txt")
			g.Assert(err).IsNotNil()
			g.Assert(IsErrorCode(err, ErrCodePathResolution)).IsTrue()
		})

		g.It("should return an error if the source is a directory", func() {
			err := os.Mkdir(filepath.Join(rfs.root, "/server/dir"), 0o755)
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

			_, err = rfs.StatServerFile("source.txt")
			g.Assert(err).IsNil()

			_, err = rfs.StatServerFile("source copy.txt")
			g.Assert(err).IsNil()
		})

		g.It("should create a copy of the file with a suffix if a copy already exists", func() {
			err := fs.Copy("source.txt")
			g.Assert(err).IsNil()

			err = fs.Copy("source.txt")
			g.Assert(err).IsNil()

			r := []string{"source.txt", "source copy.txt", "source copy 1.txt"}

			for _, name := range r {
				_, err = rfs.StatServerFile(name)
				g.Assert(err).IsNil()
			}

			g.Assert(atomic.LoadInt64(&fs.diskUsed)).Equal(int64(utf8.RuneCountInString("test content")) * 3)
		})

		g.It("should create a copy inside of a directory", func() {
			err := os.MkdirAll(filepath.Join(rfs.root, "/server/nested/in/dir"), 0o755)
			g.Assert(err).IsNil()

			err = rfs.CreateServerFileFromString("nested/in/dir/source.txt", "test content")
			g.Assert(err).IsNil()

			err = fs.Copy("nested/in/dir/source.txt")
			g.Assert(err).IsNil()

			_, err = rfs.StatServerFile("nested/in/dir/source.txt")
			g.Assert(err).IsNil()

			_, err = rfs.StatServerFile("nested/in/dir/source copy.txt")
			g.Assert(err).IsNil()
		})

		g.AfterEach(func() {
			rfs.reset()

			atomic.StoreInt64(&fs.diskUsed, 0)
			atomic.StoreInt64(&fs.diskLimit, 0)
		})
	})
}

func TestFilesystem_Delete(t *testing.T) {
	g := Goblin(t)
	fs, rfs := NewFs()

	g.Describe("Delete", func() {
		g.BeforeEach(func() {
			if err := rfs.CreateServerFileFromString("source.txt", "test content"); err != nil {
				panic(err)
			}

			atomic.StoreInt64(&fs.diskUsed, int64(utf8.RuneCountInString("test content")))
		})

		g.It("does not delete files outside the root directory", func() {
			err := rfs.CreateServerFileFromString("/../ext-source.txt", "external content")

			err = fs.Delete("../ext-source.txt")
			g.Assert(err).IsNotNil()
			g.Assert(IsErrorCode(err, ErrCodePathResolution)).IsTrue()
		})

		g.It("does not allow the deletion of the root directory", func() {
			err := fs.Delete("/")
			g.Assert(err).IsNotNil()
			g.Assert(err.Error()).Equal("cannot delete root server directory")
		})

		g.It("does not return an error if the target does not exist", func() {
			err := fs.Delete("missing.txt")
			g.Assert(err).IsNil()

			st, err := rfs.StatServerFile("source.txt")
			g.Assert(err).IsNil()
			g.Assert(st.Name()).Equal("source.txt")
		})

		g.It("deletes files and subtracts their size from the disk usage", func() {
			err := fs.Delete("source.txt")
			g.Assert(err).IsNil()

			_, err = rfs.StatServerFile("source.txt")
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

			err := os.MkdirAll(filepath.Join(rfs.root, "/server/foo/bar/baz"), 0o755)
			g.Assert(err).IsNil()

			for _, s := range sources {
				err = rfs.CreateServerFileFromString(s, "test content")
				g.Assert(err).IsNil()
			}

			atomic.StoreInt64(&fs.diskUsed, int64(utf8.RuneCountInString("test content")*3))

			err = fs.Delete("foo")
			g.Assert(err).IsNil()
			g.Assert(atomic.LoadInt64(&fs.diskUsed)).Equal(int64(0))

			for _, s := range sources {
				_, err = rfs.StatServerFile(s)
				g.Assert(err).IsNotNil()
				g.Assert(errors.Is(err, os.ErrNotExist)).IsTrue()
			}
		})

		g.It("deletes a symlink but not it's target within the root directory", func() {
			// Symlink to a file inside the root directory.
			err := os.Symlink(filepath.Join(rfs.root, "server/source.txt"), filepath.Join(rfs.root, "server/symlink.txt"))
			g.Assert(err).IsNil()

			// Delete the symlink itself.
			err = fs.Delete("symlink.txt")
			g.Assert(err).IsNil()

			// Ensure the symlink was deleted.
			_, err = os.Lstat(filepath.Join(rfs.root, "server/symlink.txt"))
			g.Assert(err).IsNotNil()

			// Ensure the symlink target still exists.
			_, err = os.Lstat(filepath.Join(rfs.root, "server/source.txt"))
			g.Assert(err).IsNil()
		})

		g.It("does not delete files symlinked outside of the root directory", func() {
			// Create a file outside the root directory.
			err := rfs.CreateServerFileFromString("/../source.txt", "test content")
			g.Assert(err).IsNil()

			// Create a symlink to the file outside the root directory.
			err = os.Symlink(filepath.Join(rfs.root, "source.txt"), filepath.Join(rfs.root, "/server/symlink.txt"))
			g.Assert(err).IsNil()

			// Delete the symlink. (This should pass as we will delete the symlink itself, not it's target)
			err = fs.Delete("symlink.txt")
			g.Assert(err).IsNil()

			// Ensure the file outside the root directory still exists.
			_, err = os.Lstat(filepath.Join(rfs.root, "source.txt"))
			g.Assert(err).IsNil()
		})

		g.It("does not delete files symlinked through a directory outside of the root directory", func() {
			// Create a directory outside the root directory.
			err := os.Mkdir(filepath.Join(rfs.root, "foo"), 0o755)
			g.Assert(err).IsNil()

			// Create a file inside the directory that is outside the root.
			err = rfs.CreateServerFileFromString("/../foo/source.txt", "test content")
			g.Assert(err).IsNil()

			// Symlink the directory that is outside the root to a file inside the root.
			err = os.Symlink(filepath.Join(rfs.root, "foo"), filepath.Join(rfs.root, "server/symlink"))
			g.Assert(err).IsNil()

			// Delete a file inside the symlinked directory.
			err = fs.Delete("symlink/source.txt")
			g.Assert(err).IsNotNil()
			g.Assert(IsErrorCode(err, ErrCodePathResolution)).IsTrue()

			// Ensure the file outside the root directory still exists.
			_, err = os.Lstat(filepath.Join(rfs.root, "foo/source.txt"))
			g.Assert(err).IsNil()
		})

		g.It("returns an error when trying to delete a non-existent file symlinked through a directory outside of the root directory", func() {
			// Create a directory outside the root directory.
			err := os.Mkdir(filepath.Join(rfs.root, "foo2"), 0o755)
			g.Assert(err).IsNil()

			// Symlink the directory that is outside the root to a file inside the root.
			err = os.Symlink(filepath.Join(rfs.root, "foo2"), filepath.Join(rfs.root, "server/symlink"))
			g.Assert(err).IsNil()

			// Delete a file inside the symlinked directory.
			err = fs.Delete("symlink/source.txt")
			g.Assert(err).IsNotNil()
			g.Assert(IsErrorCode(err, ErrCodePathResolution)).IsTrue()
		})

		g.AfterEach(func() {
			rfs.reset()

			atomic.StoreInt64(&fs.diskUsed, 0)
			atomic.StoreInt64(&fs.diskLimit, 0)
		})
	})
}
