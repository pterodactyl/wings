package filesystem

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/pterodactyl/wings/internal/ufs"
)

// Writing through a link to a file outside the root fails and leaves that file
// unchanged.
func TestWriteThroughLinkOutsideRootFails(t *testing.T) {
	fs, rfs := NewFs()
	fs.SetDiskLimit(100)

	target := filepath.Join(rfs.root, "file.bin")
	require.NoError(t, os.WriteFile(target, nil, 0o644))
	require.NoError(t, os.Symlink(target, filepath.Join(rfs.root, "server", "link")))

	err := fs.Write("link", bytes.NewReader(make([]byte, 200)), 200, 0o644)
	require.ErrorIs(t, err, ufs.ErrBadPathResolution)

	content, err := os.ReadFile(target)
	require.NoError(t, err)
	require.Empty(t, content)
}

// Creating a directory where the name is a link to a directory outside the root
// fails.
func TestCreateDirectoryThroughLinkRefusesOutsideDirectory(t *testing.T) {
	fs, rfs := NewFs()

	outsideDir := filepath.Join(rfs.root, "outside-dir")
	require.NoError(t, os.Mkdir(outsideDir, 0o755))
	require.NoError(t, os.Symlink(outsideDir, filepath.Join(rfs.root, "server", "dir-link")))

	require.Error(t, fs.CreateDirectory("dir-link", "/"))
}
