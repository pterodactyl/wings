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
	"github.com/mholt/archiver/v4"
)

func TestArchive_Stream(t *testing.T) {
	g := Goblin(t)
	fs, rfs := NewFs()

	g.Describe("Archive", func() {
		g.AfterEach(func() {
			// Reset the filesystem after each run.
			rfs.reset()
		})

		g.It("throws an error when passed invalid file paths", func() {
			a := &Archive{
				BasePath: fs.Path(),
				Files: []string{
					// To use the archiver properly, this needs to be filepath.Join(BasePath, "yeet")
					// However, this test tests that we actually validate that behavior.
					"yeet",
				},
			}

			g.Assert(a.Create(context.Background(), "")).IsNotNil()
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

			a := &Archive{
				BasePath: fs.Path(),
				Files: []string{
					filepath.Join(fs.Path(), "test"),
					filepath.Join(fs.Path(), "test_file.txt"),
				},
			}

			// Create the archive.
			archivePath := filepath.Join(rfs.root, "archive.tar.gz")
			g.Assert(a.Create(context.Background(), archivePath)).IsNil()

			// Ensure the archive exists.
			_, err = os.Stat(archivePath)
			g.Assert(err).IsNil()

			// Open the archive.
			genericFs, err := archiver.FileSystem(context.Background(), archivePath)
			g.Assert(err).IsNil()

			// Assert that we are opening an archive.
			afs, ok := genericFs.(archiver.ArchiveFS)
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

			for _, f := range files {
				v = append(v, f)
			}
			continue
		}

		v = append(v, entryName)
	}

	return v, nil
}
