package filesystem

import (
	"context"
	iofs "io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	. "github.com/franela/goblin"
	"github.com/mholt/archives"
)

func TestArchive_Stream(t *testing.T) {
	g := Goblin(t)
	fs := NewFs()

	g.Describe("Archive", func() {
		g.AfterEach(func() {
			fs.reset()
		})

		g.It("creates archive with intended files", func() {
			g.Assert(fs.CreateDirectory("test", "/")).IsNil()
			g.Assert(fs.CreateDirectory("test2", "/")).IsNil()

			err := fs.Writefile("test/file.txt", strings.NewReader("hello, world!\n"))
			g.Assert(err).IsNil()

			err = fs.Writefile("test2/file.txt", strings.NewReader("hello, world!\n"))
			g.Assert(err).IsNil()

			err = fs.Writefile("test_file.txt", strings.NewReader("hello, world!\n"))
			g.Assert(err).IsNil()

			err = fs.Writefile("test_file.txt.old", strings.NewReader("hello, world!\n"))
			g.Assert(err).IsNil()

			archivePath := filepath.Join(fs.rootPath, "../archive.tar.gz")
			f, err := os.Create(archivePath)
			if err != nil {
				panic(err)
			}
			defer f.Close()

			a, err := NewArchive(fs.root, ".", WithMatching([]string{"test", "test_file.txt"}))

			g.Assert(a.Create(context.Background(), f)).IsNil()

			// Open the archive.
			genericFs, err := archives.FileSystem(context.Background(), archivePath, nil)
			g.Assert(err).IsNil()

			// Assert that we are opening an archive.
			afs, ok := genericFs.(iofs.ReadDirFS)
			g.Assert(ok).IsTrue()

			// Get the names of the files recursively from the archive.
			files, err := getFiles(afs, ".")
			g.Assert(err).IsNil()

			// Ensure the files in the archive match what we are expecting.
			expected := []string{
				"test_file.txt",
				"test/file.txt",
			}

			// Sort the slices to ensure the comparison never fails if the
			// contents are sorted differently.
			sort.Strings(expected)
			sort.Strings(files)

			g.Assert(files).Equal(expected)
		})

		g.It("does not archive files outside of root", func() {
			if err := os.MkdirAll(filepath.Join(fs.rootPath, "../outer"), 0o755); err != nil {
				panic(err)
			}

			fs.write("test.txt", []byte("test"))
			fs.write("../danger-1.txt", []byte("danger"))
			fs.write("../outer/danger-2.txt", []byte("danger"))

			if err := os.Symlink("../danger-1.txt", filepath.Join(fs.rootPath, "symlink.txt")); err != nil {
				panic(err)
			}

			if err := os.Symlink("../outer", filepath.Join(fs.rootPath, "danger-dir")); err != nil {
				panic(err)
			}

			archivePath := filepath.Join(fs.rootPath, "../archive.tar.gz")
			f, err := os.Create(archivePath)
			if err != nil {
				panic(err)
			}
			defer f.Close()

			a, err := NewArchive(fs.root, ".")
			if err != nil {
				panic(err)
			}

			err = a.Create(context.Background(), f)
			g.Assert(err).IsNil()

			// Open the archive.
			genericFs, err := archives.FileSystem(context.Background(), archivePath, nil)
			g.Assert(err).IsNil()

			// Assert that we are opening an archive.
			afs, ok := genericFs.(iofs.ReadDirFS)
			g.Assert(ok).IsTrue()

			// Get the names of the files recursively from the archive.
			files, err := getFiles(afs, ".")
			g.Assert(err).IsNil()
			// We expect the actual symlinks themselves, but not the contents of the directory
			// or the file itself. We're storing the symlinked file in the archive so that
			// expanding it back is the same, but you won't have the inner contents.
			g.Assert(files).Equal([]string{"danger-dir", "symlink.txt", "test.txt"})
		})
	})
}

func getFiles(f iofs.ReadDirFS, name string) ([]string, error) {
	var v []string

	entries, err := f.ReadDir(name)
	if err != nil {
		return nil, err
	}

	for _, e := range entries {
		entryName := e.Name()
		if name != "." {
			entryName = filepath.Join(name, entryName)
		}

		if e.IsDir() {
			files, err := getFiles(f, entryName)
			if err != nil {
				return nil, err
			}

			if files == nil {
				return nil, nil
			}

			v = append(v, files...)
			continue
		}

		v = append(v, entryName)
	}

	return v, nil
}
