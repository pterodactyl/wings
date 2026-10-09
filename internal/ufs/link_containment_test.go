//go:build unix

package ufs_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/pterodactyl/wings/internal/ufs"
)

// newLinkedRoot returns a filesystem whose root holds symlinks to a directory and
// a file next to the root.
func newLinkedRoot(t *testing.T, openat2 bool) (*ufs.UnixFS, string) {
	t.Helper()

	base := t.TempDir()
	root := filepath.Join(base, "root")
	outside := filepath.Join(base, "outside")
	require.NoError(t, os.Mkdir(root, 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(outside, "sub"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(outside, "file.txt"), []byte("outside"), 0o644))
	require.NoError(t, os.Symlink(outside, filepath.Join(root, "link")))
	require.NoError(t, os.Symlink(filepath.Join(outside, "file.txt"), filepath.Join(root, "filelink")))

	fs, err := ufs.NewUnixFS(root, openat2)
	require.NoError(t, err)

	return fs, outside
}

// openDescriptorCount returns the number of file descriptors this process holds.
func openDescriptorCount(t *testing.T) int {
	t.Helper()

	entries, err := os.ReadDir("/proc/self/fd")
	require.NoError(t, err)

	return len(entries)
}

// Stat does not describe a file outside the root that a symlink points at.
func TestStatDoesNotFollowLinksOutsideRoot(t *testing.T) {
	for _, mode := range []struct {
		name    string
		openat2 bool
	}{{"openat2", true}, {"openat", false}} {
		t.Run(mode.name, func(t *testing.T) {
			fs, _ := newLinkedRoot(t, mode.openat2)

			for _, name := range []string{"link", "filelink"} {
				_, err := fs.Stat(name)
				require.ErrorIs(t, err, ufs.ErrBadPathResolution, name)
			}
		})
	}
}

// Stat still follows symlinks that stay inside the root.
func TestStatFollowsLinksInsideRoot(t *testing.T) {
	for _, mode := range []struct {
		name    string
		openat2 bool
	}{{"openat2", true}, {"openat", false}} {
		t.Run(mode.name, func(t *testing.T) {
			fs, _ := newLinkedRoot(t, mode.openat2)
			root := fs.BasePath()
			require.NoError(t, os.MkdirAll(filepath.Join(root, "shared", "plugins"), 0o755))
			require.NoError(t, os.WriteFile(filepath.Join(root, "shared", "data.txt"), []byte("12345"), 0o644))
			require.NoError(t, os.Mkdir(filepath.Join(root, "server"), 0o755))
			require.NoError(t, os.Symlink("../shared/plugins", filepath.Join(root, "server", "plugins")))
			require.NoError(t, os.Symlink("shared/data.txt", filepath.Join(root, "rel-link")))

			st, err := fs.Stat("server/plugins")
			require.NoError(t, err)
			require.True(t, st.IsDir())

			st, err = fs.Stat("rel-link")
			require.NoError(t, err)
			require.Equal(t, int64(5), st.Size())
		})
	}
}

// A parent directory that resolves outside the root is rejected, and its
// descriptor is closed.
func TestRejectedParentReleasesDescriptor(t *testing.T) {
	fs, _ := newLinkedRoot(t, false)

	before := openDescriptorCount(t)
	for range 100 {
		_, err := fs.Lstat("link/sub/file.txt")
		require.ErrorIs(t, err, ufs.ErrBadPathResolution)
	}
	require.LessOrEqual(t, openDescriptorCount(t)-before, 5)
}

// Touch through a link to a directory outside the root fails.
func TestTouchThroughLinkCreatesNothingOutsideRoot(t *testing.T) {
	for _, mode := range []struct {
		name    string
		openat2 bool
	}{{"openat2", true}, {"openat", false}} {
		t.Run(mode.name, func(t *testing.T) {
			fs, outside := newLinkedRoot(t, mode.openat2)

			_, err := fs.Touch("link/sub/new.txt", ufs.O_RDWR|ufs.O_TRUNC, 0o644)
			require.Error(t, err)
			require.NoFileExists(t, filepath.Join(outside, "sub", "new.txt"))
		})
	}
}

// Open and Touch through a link to a directory outside the root fail.
func TestOpenAndTruncateThroughLinkLeaveOutsideFileUnchanged(t *testing.T) {
	for _, mode := range []struct {
		name    string
		openat2 bool
	}{{"openat2", true}, {"openat", false}} {
		t.Run(mode.name, func(t *testing.T) {
			fs, outside := newLinkedRoot(t, mode.openat2)
			require.NoError(t, os.WriteFile(filepath.Join(outside, "sub", "existing.txt"), []byte("outside"), 0o644))

			_, err := fs.Open("link/sub/existing.txt")
			require.Error(t, err)

			_, err = fs.Touch("link/sub/existing.txt", ufs.O_RDWR|ufs.O_TRUNC, 0o644)
			require.Error(t, err)

			content, err := os.ReadFile(filepath.Join(outside, "sub", "existing.txt"))
			require.NoError(t, err)
			require.Equal(t, "outside", string(content))
		})
	}
}

// MkdirAll through a link to a directory outside the root fails.
func TestMkdirAllThroughLinkCreatesNothingOutsideRoot(t *testing.T) {
	for _, mode := range []struct {
		name    string
		openat2 bool
	}{{"openat2", true}, {"openat", false}} {
		t.Run(mode.name, func(t *testing.T) {
			fs, outside := newLinkedRoot(t, mode.openat2)

			_, err := fs.MkdirAll("link/newdir/child", 0o755)
			require.Error(t, err)
			require.NoDirExists(t, filepath.Join(outside, "newdir"))
		})
	}
}

// Changing the mode of a link leaves the file it points at unchanged.
func TestChmodThroughLinkLeavesOutsideFileUnchanged(t *testing.T) {
	fs, outside := newLinkedRoot(t, true)

	_ = fs.Chmod("filelink", 0o600)

	st, err := os.Stat(filepath.Join(outside, "file.txt"))
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o644), st.Mode().Perm())
}

// RemoveAll through a link only removes the link.
func TestRemoveAllThroughLinkKeepsOutsideContents(t *testing.T) {
	fs, outside := newLinkedRoot(t, true)

	_ = fs.RemoveAll("link/sub")
	require.DirExists(t, filepath.Join(outside, "sub"))

	_ = fs.RemoveAll("link")
	require.FileExists(t, filepath.Join(outside, "file.txt"))
	require.DirExists(t, filepath.Join(outside, "sub"))
}
