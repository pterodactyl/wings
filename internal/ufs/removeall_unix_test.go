//go:build unix

package ufs

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

// failingUnlinkFS fails to remove any entry with the given name.
type failingUnlinkFS struct {
	*UnixFS
	name string
}

func (fs failingUnlinkFS) unlinkat(dirfd int, name string, flags int) error {
	if name == fs.name {
		return unix.EBUSY
	}
	return fs.UnixFS.unlinkat(dirfd, name, flags)
}

func TestRemoveReportsEntriesThatCouldNotBeRemoved(t *testing.T) {
	root := t.TempDir()
	for _, p := range []string{"dir/keep", "dir/remove"} {
		if err := os.MkdirAll(filepath.Join(root, filepath.Dir(p)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, p), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	unixFS, err := NewUnixFS(root, false)
	if err != nil {
		t.Fatal(err)
	}
	defer unixFS.Close()
	fs := failingUnlinkFS{UnixFS: unixFS, name: "keep"}

	if err := removeContents(fs, "dir"); !errors.Is(err, unix.EBUSY) {
		t.Fatalf("expected removing the contents to report the entry that could not be removed, got %v", err)
	}
	if err := removeAll(fs, "dir"); !errors.Is(err, unix.EBUSY) {
		t.Fatalf("expected removing the directory to report the entry that could not be removed, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "dir/remove")); !os.IsNotExist(err) {
		t.Fatal("expected the other entry to be removed")
	}
}
