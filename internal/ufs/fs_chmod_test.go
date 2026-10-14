//go:build unix

package ufs

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

// The fallback used where fchmodat2 is unavailable changes the mode of a file
// and refuses to follow a symbolic link.
func TestChmodWithoutFchmodat2(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outside, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "file.txt"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}

	dirfd, err := unix.Open(root, unix.O_DIRECTORY|unix.O_RDONLY|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer unix.Close(dirfd)

	if err := fchmodNoFollow(dirfd, "file.txt", 0o600); err != nil {
		t.Fatalf("expected the mode to be changed, got %v", err)
	}
	if st, err := os.Stat(filepath.Join(root, "file.txt")); err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("expected mode 0600, got %v (%v)", st.Mode().Perm(), err)
	}

	if err := fchmodNoFollow(dirfd, "link", 0o600); err == nil {
		t.Fatal("expected changing the mode of a symlink to fail")
	}
	if st, err := os.Stat(outside); err != nil || st.Mode().Perm() != 0o644 {
		t.Fatalf("expected the link target to be unchanged, got %v (%v)", st.Mode().Perm(), err)
	}
}
