package filesystem

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newArchiveTestFilesystem returns a filesystem rooted in a temporary directory.
func newArchiveTestFilesystem(t *testing.T) *Filesystem {
	t.Helper()
	fs, rfs := NewFs()
	t.Cleanup(func() { _ = os.RemoveAll(rfs.root) })
	return fs
}

// archiveEntries streams a full archive of the filesystem and returns its
// tar headers keyed by entry name.
func archiveEntries(t *testing.T, fsys *Filesystem) map[string]*tar.Header {
	t.Helper()

	var buf bytes.Buffer
	a := &Archive{Filesystem: fsys}
	require.NoError(t, a.Stream(context.Background(), &buf))

	gz, err := gzip.NewReader(&buf)
	require.NoError(t, err)
	tr := tar.NewReader(gz)

	entries := make(map[string]*tar.Header)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		require.NoError(t, err)
		entries[hdr.Name] = hdr
	}
	return entries
}

func archiveEntryNames(entries map[string]*tar.Header) []string {
	names := make([]string, 0, len(entries))
	for name := range entries {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// A symlink in a subdirectory of the server is archived under its own path, with
// its own target.
func TestArchiveKeepsSymlinkEntriesWithTheirPath(t *testing.T) {
	fsys := newArchiveTestFilesystem(t)
	root := fsys.Path()
	require.NoError(t, os.Mkdir(filepath.Join(root, "sub"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "sub", "target.txt"), []byte("data"), 0o644))
	require.NoError(t, os.Symlink("target.txt", filepath.Join(root, "sub", "link")))

	entries := archiveEntries(t, fsys)

	entry, ok := entries["sub/link"]
	require.True(t, ok, "symlink sub/link is missing from the archive; entries: %v", archiveEntryNames(entries))
	assert.Equal(t, byte(tar.TypeSymlink), entry.Typeflag)
	assert.Equal(t, "target.txt", entry.Linkname)
}

// The link target is read from the link inside the server.
func TestArchiveReadsSymlinkTargetFromServer(t *testing.T) {
	fsys := newArchiveTestFilesystem(t)
	root := fsys.Path()
	require.NoError(t, os.WriteFile(filepath.Join(root, "target.txt"), []byte("data"), 0o644))
	require.NoError(t, os.Symlink("target.txt", filepath.Join(root, "link")))

	// A link with the same name in the working directory.
	cwd := t.TempDir()
	require.NoError(t, os.Symlink("other.txt", filepath.Join(cwd, "link")))
	t.Chdir(cwd)

	entries := archiveEntries(t, fsys)

	entry, ok := entries["link"]
	require.True(t, ok, "symlink link is missing from the archive; entries: %v", archiveEntryNames(entries))
	assert.Equal(t, "target.txt", entry.Linkname)
}
