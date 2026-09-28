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
	fs, rfs := NewFs()

	g.Describe("Archive", func() {
		g.AfterEach(func() {
			// Reset the filesystem after each run.
			_ = fs.TruncateRootDirectory()
		})

		g.It("creates archive with intended files", func() {
			g.Assert(fs.CreateDirectory("test", "/")).IsNil()
			g.Assert(fs.CreateDirectory("test2", "/")).IsNil()

			r := strings.NewReader("hello, world!\n")
			err := fs.Write("test/file.txt", r, r.Size(), 0o644)
			g.Assert(err).IsNil()

			r = strings.NewReader("hello, world!\n")
			err = fs.Write("test2/file.txt", r, r.Size(), 0o644)
			g.Assert(err).IsNil()

			r = strings.NewReader("hello, world!\n")
			err = fs.Write("test_file.txt", r, r.Size(), 0o644)
			g.Assert(err).IsNil()

			r = strings.NewReader("hello, world!\n")
			err = fs.Write("test_file.txt.old", r, r.Size(), 0o644)
			g.Assert(err).IsNil()

			a := &Archive{
				Filesystem: fs,
				Files: []string{
					"test",
					"test_file.txt",
				},
			}

			// Create the archive.
			archivePath := filepath.Join(rfs.root, "archive.tar.gz")
			g.Assert(a.Create(context.Background(), archivePath)).IsNil()

			// Ensure the archive exists.
			_, err = os.Stat(archivePath)
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

		g.It("treats regular expression metacharacters in ignore patterns literally", func() {
			g.Assert(fs.CreateDirectory("world (copy)", "/")).IsNil()
			g.Assert(fs.CreateDirectory("logs", "/")).IsNil()
			for _, name := range []string{"world (copy)/level.dat", "logs/latest.log", "server.jar"} {
				r := strings.NewReader("hello, world!\n")
				g.Assert(fs.Write(name, r, r.Size(), 0o644)).IsNil()
			}

			a := &Archive{
				Filesystem: fs,
				Ignore:     "world (copy)/\n*.log\n",
			}

			archivePath := filepath.Join(rfs.root, "archive.tar.gz")
			g.Assert(a.Create(context.Background(), archivePath)).IsNil()

			genericFs, err := archives.FileSystem(context.Background(), archivePath, nil)
			g.Assert(err).IsNil()
			afs, ok := genericFs.(iofs.ReadDirFS)
			g.Assert(ok).IsTrue()

			files, err := getFiles(afs, ".")
			g.Assert(err).IsNil()
			g.Assert(files).Equal([]string{"server.jar"})
		})

		g.It("rejects an ignore list that exceeds the limits", func() {
			r := strings.NewReader("hello, world!\n")
			g.Assert(fs.Write("server.jar", r, r.Size(), 0o644)).IsNil()

			a := &Archive{
				Filesystem: fs,
				Ignore:     strings.Repeat("*a", MaxIgnorePatternWildcards+1),
			}

			archivePath := filepath.Join(rfs.root, "archive.tar.gz")
			g.Assert(a.Create(context.Background(), archivePath)).IsNotNil()
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
