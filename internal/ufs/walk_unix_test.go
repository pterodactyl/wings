//go:build unix

package ufs_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/pterodactyl/wings/internal/ufs"
)

// A directory that is removed after it has been listed, but before the walk
// opens it, is reported to the callback rather than ending the walk.
func TestWalkDiratReportsDirectoriesRemovedDuringWalk(t *testing.T) {
	t.Parallel()

	fs, err := newTestUnixFS()
	if err != nil {
		t.Fatal(err)
	}
	defer fs.Cleanup()

	for _, dir := range []string{"a", "b", "c"} {
		if err := os.MkdirAll(filepath.Join(fs.Root, dir, "child"), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	dirfd, name, closeFd, err := fs.SafePath("/")
	defer closeFd()
	if err != nil {
		t.Fatal(err)
	}

	var reported []string
	var visited []string
	err = fs.WalkDirat(dirfd, name, func(_ int, _, relative string, d ufs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, ufs.ErrNotExist) {
				reported = append(reported, relative)
				return nil
			}
			return err
		}
		visited = append(visited, relative)
		// Remove the directory after it has been listed and before it is opened.
		if relative == "b" {
			return os.RemoveAll(filepath.Join(fs.Root, "b"))
		}
		return nil
	})
	if err != nil {
		t.Fatalf("expected the walk to continue past a removed directory, got %v", err)
	}
	if len(reported) != 1 || reported[0] != "b" {
		t.Fatalf("expected the removed directory to be reported, got %v", reported)
	}
	for _, want := range []string{"a/child", "c/child"} {
		found := false
		for _, v := range visited {
			found = found || v == want
		}
		if !found {
			t.Fatalf("expected %s to be visited, visited %v", want, visited)
		}
	}
}
