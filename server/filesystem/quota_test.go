package filesystem

import (
	"bytes"
	"crypto/rand"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/pterodactyl/wings/internal/ufs"
)

// diskUsage returns the actual size of the files in the server's directory.
func diskUsage(t *testing.T, rfs *rootFs) int64 {
	t.Helper()
	var size int64
	err := filepath.Walk(filepath.Join(rfs.root, "server"), func(_ string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.Mode().IsRegular() {
			size += info.Size()
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return size
}

func randomData(t *testing.T, n int) []byte {
	t.Helper()
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return b
}

// limitWithRoomFor sets the disk limit to the current usage plus the given number
// of bytes, returning the limit.
func limitWithRoomFor(t *testing.T, fs *Filesystem, n int64) int64 {
	t.Helper()
	usage, err := fs.DiskUsage(false)
	if err != nil {
		t.Fatal(err)
	}
	fs.SetDiskLimit(usage + n)
	return usage + n
}

func TestParallelArchivesRespectDiskLimit(t *testing.T) {
	fs, rfs := NewFs()
	t.Cleanup(func() { _ = os.RemoveAll(rfs.root) })

	const count = 8
	for i := 0; i < count; i++ {
		dir := fmt.Sprintf("dir%d", i)
		if err := os.MkdirAll(filepath.Join(rfs.root, "server", dir), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := rfs.CreateServerFile(filepath.Join(dir, "data.bin"), randomData(t, 1<<20)); err != nil {
			t.Fatal(err)
		}
	}
	// Enough room for one more archive of the (incompressible) data, but not two.
	limit := limitWithRoomFor(t, fs, 1<<20+512<<10)

	var wg sync.WaitGroup
	for i := 0; i < count; i++ {
		wg.Add(1)
		go func(dir string) {
			defer wg.Done()
			_, _ = fs.CompressFiles(dir, []string{"data.bin"})
		}(fmt.Sprintf("/dir%d", i))
	}
	wg.Wait()

	if used := diskUsage(t, rfs); used > limit {
		t.Fatalf("parallel archives used %d bytes, exceeding the disk limit of %d bytes", used, limit)
	}
	if used, tracked := diskUsage(t, rfs), fs.CachedUsage(); used != tracked {
		t.Fatalf("tracked disk usage %d does not match the actual usage %d", tracked, used)
	}
}

func TestParallelCopiesRespectDiskLimit(t *testing.T) {
	fs, rfs := NewFs()
	t.Cleanup(func() { _ = os.RemoveAll(rfs.root) })

	if err := rfs.CreateServerFile("data.bin", randomData(t, 1<<20)); err != nil {
		t.Fatal(err)
	}
	limit := limitWithRoomFor(t, fs, 1<<20+512<<10)

	const count = 8
	var wg sync.WaitGroup
	var copied atomic.Int64
	for i := 0; i < count; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if fs.Copy("data.bin") == nil {
				copied.Add(1)
			}
		}()
	}
	wg.Wait()

	if used := diskUsage(t, rfs); used > limit {
		t.Fatalf("parallel copies used %d bytes, exceeding the disk limit of %d bytes", used, limit)
	}
	if used, tracked := diskUsage(t, rfs), fs.CachedUsage(); used != tracked {
		t.Fatalf("tracked disk usage %d does not match the actual usage %d", tracked, used)
	}
	entries, err := os.ReadDir(filepath.Join(rfs.root, "server"))
	if err != nil {
		t.Fatal(err)
	}
	if int64(len(entries)) != 1+copied.Load() {
		t.Fatalf("expected %d files, got %d: copies were written into the same file", 1+copied.Load(), len(entries))
	}
}

// Recalculating the disk usage while a write is in progress must not lose the
// space reserved for it, otherwise the next write could use that space as well.
func TestDiskUsageRecalculationKeepsReservations(t *testing.T) {
	fs, rfs := NewFs()
	t.Cleanup(func() { _ = os.RemoveAll(rfs.root) })

	limit := limitWithRoomFor(t, fs, 1<<20+512<<10)

	data := randomData(t, 1<<20)
	r, w := io.Pipe()
	done := make(chan error, 1)
	go func() {
		done <- fs.Write("slow.bin", r, int64(len(data)), 0o644)
	}()
	// Send half of the file, then recalculate the disk usage while the rest of it
	// is still on its way.
	if _, err := w.Write(data[:len(data)/2]); err != nil {
		t.Fatal(err)
	}
	if _, err := fs.updateCachedDiskUsage(); err != nil {
		t.Fatal(err)
	}

	err := fs.Write("second.bin", bytes.NewReader(data), int64(len(data)), 0o644)
	if !IsErrorCode(err, ErrCodeDiskSpace) {
		t.Fatalf("expected the second write to be refused, got %v", err)
	}

	if _, err := w.Write(data[len(data)/2:]); err != nil {
		t.Fatal(err)
	}
	_ = w.Close()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if used := diskUsage(t, rfs); used > limit {
		t.Fatalf("writes used %d bytes, exceeding the disk limit of %d bytes", used, limit)
	}
}

func TestParallelWritesRespectDiskLimit(t *testing.T) {
	fs, rfs := NewFs()
	t.Cleanup(func() { _ = os.RemoveAll(rfs.root) })

	if err := rfs.CreateServerFile("existing.bin", randomData(t, 1<<20)); err != nil {
		t.Fatal(err)
	}
	limit := limitWithRoomFor(t, fs, 1<<20+512<<10)

	const count = 8
	data := randomData(t, 1<<20)
	var wg sync.WaitGroup
	for i := 0; i < count; i++ {
		wg.Add(1)
		go func(p string) {
			defer wg.Done()
			_ = fs.Write(p, bytes.NewReader(data), int64(len(data)), 0o644)
		}(fmt.Sprintf("file%d.bin", i))
	}
	wg.Wait()

	if used := diskUsage(t, rfs); used > limit {
		t.Fatalf("parallel writes used %d bytes, exceeding the disk limit of %d bytes", used, limit)
	}
	if used, tracked := diskUsage(t, rfs), fs.CachedUsage(); used != tracked {
		t.Fatalf("tracked disk usage %d does not match the actual usage %d", tracked, used)
	}
}

// Writes that finish while the disk usage is being recalculated were already
// counted by the walk, so they must not be counted again.
func TestWritesFinishedDuringDiskUsageWalkAreCountedOnce(t *testing.T) {
	fs, rfs := NewFs()
	t.Cleanup(func() { _ = os.RemoveAll(rfs.root) })
	root := filepath.Join(rfs.root, "server")

	// A directory with many files keeps the walk busy while the write happens.
	if err := os.MkdirAll(filepath.Join(root, "slow"), 0o755); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 100000; i++ {
		if err := os.WriteFile(filepath.Join(root, "slow", fmt.Sprintf("f%d", i)), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// Write to a file the walk only reaches after the slow directory, so that the
	// walk counts all of it.
	for i := 0; i < 8; i++ {
		if err := os.WriteFile(filepath.Join(root, fmt.Sprintf("z%d.bin", i)), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	d, err := os.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	names, _ := d.Readdirnames(-1)
	_ = d.Close()
	var target string
	for i, n := range names {
		if n == "slow" && i+1 < len(names) {
			target = names[i+1]
		}
	}
	if target == "" {
		t.Skip("no file is walked after the slow directory")
	}
	fs.SetDiskLimit(1 << 30)
	if _, err := fs.updateCachedDiskUsage(); err != nil {
		t.Fatal(err)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = fs.updateCachedDiskUsage()
	}()
	for !fs.lookupInProgress.Load() {
	}
	// Write through Touch as SFTP does, which reserves space for every chunk.
	f, err := fs.Touch(target, ufs.O_RDWR|ufs.O_TRUNC)
	if err != nil {
		t.Fatal(err)
	}
	chunk := bytes.Repeat([]byte{1}, 32<<10)
	for n := 0; n < 16<<20; n += len(chunk) {
		if _, err := f.Write(chunk); err != nil {
			t.Fatal(err)
		}
	}
	_ = f.Close()
	if !fs.lookupInProgress.Load() {
		t.Skip("the walk finished before the write did")
	}
	<-done

	if used, tracked := diskUsage(t, rfs), fs.CachedUsage(); used != tracked {
		t.Fatalf("tracked disk usage %d does not match the actual usage %d", tracked, used)
	}
}

type panicReader struct{}

func (panicReader) Read([]byte) (int, error) { panic("read failed") }

// A panic while writing is recovered by the HTTP server, so the space reserved
// for the write must still be released.
func TestWriteReleasesReservedSpaceWhenItPanics(t *testing.T) {
	fs, rfs := NewFs()
	t.Cleanup(func() { _ = os.RemoveAll(rfs.root) })
	limitWithRoomFor(t, fs, 1<<20)

	func() {
		defer func() { _ = recover() }()
		_ = fs.Write("panic.bin", panicReader{}, 1<<20, 0o644)
	}()

	if err := fs.Write("after.bin", bytes.NewReader(make([]byte, 1<<20)), 1<<20, 0o644); err != nil {
		t.Fatalf("expected the space reserved by the write that panicked to be released, got %v", err)
	}
}
