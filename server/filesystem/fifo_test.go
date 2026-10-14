package filesystem

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/pterodactyl/wings/internal/ufs"
)

// File operations return an error for named pipes rather than opening them.
func TestNamedPipesAreNeverOpened(t *testing.T) {
	fs, rfs := NewFs()
	t.Cleanup(func() { _ = os.RemoveAll(rfs.root) })
	fs.SetDiskLimit(1024 * 1024 * 1024)

	ops := []struct {
		name string
		op   func(p string) error
	}{
		{"File", func(p string) error {
			f, _, err := fs.File(p)
			if err == nil {
				_ = f.Close()
			}
			return err
		}},
		{"Touch", func(p string) error {
			f, err := fs.Touch(p, ufs.O_RDWR)
			if err == nil {
				_ = f.Close()
			}
			return err
		}},
		{"Write", func(p string) error {
			b := make([]byte, 1<<20)
			return fs.Write(p, bytes.NewReader(b), int64(len(b)), 0o644)
		}},
		{"Copy", func(p string) error { return fs.Copy(p) }},
		{"DecompressFile", func(p string) error { return fs.DecompressFile(context.Background(), "/", p) }},
		{"SpaceAvailableForDecompression", func(p string) error {
			return fs.SpaceAvailableForDecompression(context.Background(), "/", p)
		}},
	}

	for _, tt := range ops {
		t.Run(tt.name, func(t *testing.T) {
			// Opening a pipe for writing unblocks anything waiting to read from it,
			// so each operation gets a pipe of its own.
			p := "pipe-" + tt.name
			if err := unix.Mkfifo(filepath.Join(rfs.root, "server", p), 0o644); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() { done <- tt.op(p) }()
			select {
			case err := <-done:
				if err == nil {
					t.Fatal("expected an error for a named pipe")
				}
			case <-time.After(time.Second * 2):
				t.Fatal("blocked on a named pipe")
			}
		})
	}
}
