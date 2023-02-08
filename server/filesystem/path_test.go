package filesystem

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"emperror.dev/errors"
	. "github.com/franela/goblin"
)

func TestFilesystem_Path(t *testing.T) {
	g := Goblin(t)
	fs, rfs := NewFs()

	g.Describe("Path", func() {
		g.It("returns the root path for the instance", func() {
			g.Assert(fs.Path()).Equal(filepath.Join(rfs.root, "/server"))
		})
	})
}

func TestFilesystem_SafePath(t *testing.T) {
	g := Goblin(t)
	fs, rfs := NewFs()
	prefix := filepath.Join(rfs.root, "/server")

	g.Describe("SafePath", func() {
		g.It("returns a cleaned path to a given file", func() {
			p, err := fs.SafePath("test.txt")
			g.Assert(err).IsNil()
			g.Assert(p).Equal(prefix + "/test.txt")

			p, err = fs.SafePath("/test.txt")
			g.Assert(err).IsNil()
			g.Assert(p).Equal(prefix + "/test.txt")

			p, err = fs.SafePath("./test.txt")
			g.Assert(err).IsNil()
			g.Assert(p).Equal(prefix + "/test.txt")

			p, err = fs.SafePath("/foo/../test.txt")
			g.Assert(err).IsNil()
			g.Assert(p).Equal(prefix + "/test.txt")

			p, err = fs.SafePath("/foo/bar")
			g.Assert(err).IsNil()
			g.Assert(p).Equal(prefix + "/foo/bar")
		})

		g.It("handles root directory access", func() {
			p, err := fs.SafePath("/")
			g.Assert(err).IsNil()
			g.Assert(p).Equal(prefix)

			p, err = fs.SafePath("")
			g.Assert(err).IsNil()
			g.Assert(p).Equal(prefix)
		})

		g.It("removes trailing slashes from paths", func() {
			p, err := fs.SafePath("/foo/bar/")
			g.Assert(err).IsNil()
			g.Assert(p).Equal(prefix + "/foo/bar")
		})

		g.It("handles deeply nested directories that do not exist", func() {
			p, err := fs.SafePath("/foo/bar/baz/quaz/../../ducks/testing.txt")
			g.Assert(err).IsNil()
			g.Assert(p).Equal(prefix + "/foo/bar/ducks/testing.txt")
		})

		g.It("blocks access to files outside the root directory", func() {
			p, err := fs.SafePath("../test.txt")
			g.Assert(err).IsNotNil()
			g.Assert(IsErrorCode(err, ErrCodePathResolution)).IsTrue()
			g.Assert(p).Equal("")

			p, err = fs.SafePath("/../test.txt")
			g.Assert(err).IsNotNil()
			g.Assert(IsErrorCode(err, ErrCodePathResolution)).IsTrue()
			g.Assert(p).Equal("")

			p, err = fs.SafePath("./foo/../../test.txt")
			g.Assert(err).IsNotNil()
			g.Assert(IsErrorCode(err, ErrCodePathResolution)).IsTrue()
			g.Assert(p).Equal("")

			p, err = fs.SafePath("..")
			g.Assert(err).IsNotNil()
			g.Assert(IsErrorCode(err, ErrCodePathResolution)).IsTrue()
			g.Assert(p).Equal("")
		})
	})
}

// We test against accessing files outside the root directory in the tests, however it
// is still possible for someone to mess up and not properly use this safe path call. In
// order to truly confirm this, we'll try to pass in a symlinked malicious file to all of
// the calls and ensure they all fail with the same reason.
func TestFilesystem_Blocks_Symlinks(t *testing.T) {
	g := Goblin(t)
	fs, rfs := NewFs()

	if err := rfs.CreateServerFileFromString("/../malicious.txt", "external content"); err != nil {
		panic(err)
	}

	if err := os.Mkdir(filepath.Join(rfs.root, "/malicious_dir"), 0o777); err != nil {
		panic(err)
	}

	if err := os.Symlink(filepath.Join(rfs.root, "malicious.txt"), filepath.Join(rfs.root, "/server/symlinked.txt")); err != nil {
		panic(err)
	}

	if err := os.Symlink(filepath.Join(rfs.root, "malicious_does_not_exist.txt"), filepath.Join(rfs.root, "/server/symlinked_does_not_exist.txt")); err != nil {
		panic(err)
	}

	if err := os.Symlink(filepath.Join(rfs.root, "/server/symlinked_does_not_exist.txt"), filepath.Join(rfs.root, "/server/symlinked_does_not_exist2.txt")); err != nil {
		panic(err)
	}

	if err := os.Symlink(filepath.Join(rfs.root, "/malicious_dir"), filepath.Join(rfs.root, "/server/external_dir")); err != nil {
		panic(err)
	}

	g.Describe("Writefile", func() {
		g.It("cannot write to a file symlinked outside the root", func() {
			r := bytes.NewReader([]byte("testing"))

			err := fs.Writefile("symlinked.txt", r)
			g.Assert(err).IsNotNil()
			g.Assert(IsErrorCode(err, ErrCodePathResolution)).IsTrue()
		})

		g.It("cannot write to a non-existent file symlinked outside the root", func() {
			r := bytes.NewReader([]byte("testing what the fuck"))

			err := fs.Writefile("symlinked_does_not_exist.txt", r)
			g.Assert(err).IsNotNil()
			g.Assert(IsErrorCode(err, ErrCodePathResolution)).IsTrue()
		})

		g.It("cannot write to chained symlinks with target that does not exist outside the root", func() {
			r := bytes.NewReader([]byte("testing what the fuck"))

			err := fs.Writefile("symlinked_does_not_exist2.txt", r)
			g.Assert(err).IsNotNil()
			g.Assert(IsErrorCode(err, ErrCodePathResolution)).IsTrue()
		})

		g.It("cannot write a file to a directory symlinked outside the root", func() {
			r := bytes.NewReader([]byte("testing"))

			err := fs.Writefile("external_dir/foo.txt", r)
			g.Assert(err).IsNotNil()
			g.Assert(IsErrorCode(err, ErrCodePathResolution)).IsTrue()
		})
	})

	g.Describe("CreateDirectory", func() {
		g.It("cannot create a directory outside the root", func() {
			err := fs.CreateDirectory("my_dir", "external_dir")
			g.Assert(err).IsNotNil()
			g.Assert(IsErrorCode(err, ErrCodePathResolution)).IsTrue()
		})

		g.It("cannot create a nested directory outside the root", func() {
			err := fs.CreateDirectory("my/nested/dir", "external_dir/foo/bar")
			g.Assert(err).IsNotNil()
			g.Assert(IsErrorCode(err, ErrCodePathResolution)).IsTrue()
		})

		g.It("cannot create a nested directory outside the root", func() {
			err := fs.CreateDirectory("my/nested/dir", "external_dir/server")
			g.Assert(err).IsNotNil()
			g.Assert(IsErrorCode(err, ErrCodePathResolution)).IsTrue()
		})
	})

	g.Describe("Rename", func() {
		g.It("cannot rename a file symlinked outside the directory root", func() {
			err := fs.Rename("symlinked.txt", "foo.txt")
			g.Assert(err).IsNotNil()
			g.Assert(IsErrorCode(err, ErrCodePathResolution)).IsTrue()
		})

		g.It("cannot rename a symlinked directory outside the root", func() {
			err := fs.Rename("external_dir", "foo")
			g.Assert(err).IsNotNil()
			g.Assert(IsErrorCode(err, ErrCodePathResolution)).IsTrue()
		})

		g.It("cannot rename a file to a location outside the directory root", func() {
			rfs.CreateServerFileFromString("my_file.txt", "internal content")

			err := fs.Rename("my_file.txt", "external_dir/my_file.txt")
			g.Assert(err).IsNotNil()
			g.Assert(IsErrorCode(err, ErrCodePathResolution)).IsTrue()
		})
	})

	g.Describe("Chown", func() {
		g.It("cannot chown a file symlinked outside the directory root", func() {
			err := fs.Chown("symlinked.txt")
			g.Assert(err).IsNotNil()
			g.Assert(IsErrorCode(err, ErrCodePathResolution)).IsTrue()
		})

		g.It("cannot chown a directory symlinked outside the directory root", func() {
			err := fs.Chown("external_dir")
			g.Assert(err).IsNotNil()
			g.Assert(IsErrorCode(err, ErrCodePathResolution)).IsTrue()
		})
	})

	g.Describe("Copy", func() {
		g.It("cannot copy a file symlinked outside the directory root", func() {
			err := fs.Copy("symlinked.txt")
			g.Assert(err).IsNotNil()
			g.Assert(IsErrorCode(err, ErrCodePathResolution)).IsTrue()
		})
	})

	g.Describe("Delete", func() {
		g.It("deletes the symlinked file but leaves the source", func() {
			err := fs.Delete("symlinked.txt")
			g.Assert(err).IsNil()

			_, err = os.Stat(filepath.Join(rfs.root, "malicious.txt"))
			g.Assert(err).IsNil()

			_, err = rfs.StatServerFile("symlinked.txt")
			g.Assert(err).IsNotNil()
			g.Assert(errors.Is(err, os.ErrNotExist)).IsTrue()
		})
	})

	rfs.reset()
}
