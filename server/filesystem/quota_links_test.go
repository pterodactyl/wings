package filesystem

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The disk usage counts a file once however many names it has, so deleting one
// of its names does not free any space.
func TestDeletingLinkedNamesKeepsUsageAtSizeOnDisk(t *testing.T) {
	fs, rfs := NewFs()
	t.Cleanup(func() { _ = os.RemoveAll(rfs.root) })

	const size = 8 << 10
	require.NoError(t, rfs.CreateServerFile("a", randomData(t, size)))
	base := filepath.Join(rfs.root, "server")
	for _, name := range []string{"b", "c", "d"} {
		require.NoError(t, os.Link(filepath.Join(base, "a"), filepath.Join(base, name)))
	}

	_, err := fs.DiskUsage(false)
	require.NoError(t, err)
	require.Equal(t, int64(size), fs.CachedUsage())

	for _, name := range []string{"b", "c", "d"} {
		require.NoError(t, fs.Delete(name))
	}
	assert.Equal(t, int64(size), fs.CachedUsage())

	// Removing the last name frees the space.
	require.NoError(t, fs.Delete("a"))
	assert.Equal(t, int64(0), fs.CachedUsage())
}

func TestDeletingLinkedNamesInDirectoryKeepsUsageAtSizeOnDisk(t *testing.T) {
	fs, rfs := NewFs()
	t.Cleanup(func() { _ = os.RemoveAll(rfs.root) })

	const size = 8 << 10
	require.NoError(t, rfs.CreateServerFile("a", randomData(t, size)))
	base := filepath.Join(rfs.root, "server")
	require.NoError(t, os.Mkdir(filepath.Join(base, "dir"), 0o755))
	for _, name := range []string{"b", "c"} {
		require.NoError(t, os.Link(filepath.Join(base, "a"), filepath.Join(base, "dir", name)))
	}

	_, err := fs.DiskUsage(false)
	require.NoError(t, err)

	require.NoError(t, fs.Delete("dir"))
	assert.Equal(t, int64(size), fs.CachedUsage())
	assert.Equal(t, diskUsage(t, rfs), fs.CachedUsage())
}

// After deleting linked names, a write that does not fit the space left on disk
// is still refused.
func TestDeletingLinkedNamesDoesNotAllowWritesOverLimit(t *testing.T) {
	fs, rfs := NewFs()
	t.Cleanup(func() { _ = os.RemoveAll(rfs.root) })

	const size = 8 << 10
	require.NoError(t, rfs.CreateServerFile("a", randomData(t, size)))
	base := filepath.Join(rfs.root, "server")
	for _, name := range []string{"b", "c"} {
		require.NoError(t, os.Link(filepath.Join(base, "a"), filepath.Join(base, name)))
	}

	_, err := fs.DiskUsage(false)
	require.NoError(t, err)
	for _, name := range []string{"b", "c"} {
		require.NoError(t, fs.Delete(name))
	}

	fs.SetDiskLimit(2 * size)
	err = fs.Write("new", bytes.NewReader(randomData(t, 2*size)), 2*size, 0o644)
	assert.True(t, IsErrorCode(err, ErrCodeDiskSpace), "expected the write to be refused, got %v", err)
}
