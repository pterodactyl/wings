package filesystem

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A walk that fails part of the way through has only counted some of the files,
// so it must not lower the cached usage.
func TestFailedDiskUsageWalkDoesNotLowerUsage(t *testing.T) {
	fs, rfs := NewFs()
	t.Cleanup(func() { _ = os.RemoveAll(rfs.root) })

	fs.unixFS.SetUsage(12345)
	require.NoError(t, os.RemoveAll(filepath.Join(rfs.root, "server")))

	_, err := fs.DiskUsage(false)
	require.Error(t, err)
	assert.Equal(t, int64(12345), fs.CachedUsage())
}

// A caller that found the cache stale and waited for another walk to finish
// uses that walk's result instead of walking the disk again.
func TestQueuedDiskUsageLookupUsesRefreshedCache(t *testing.T) {
	fs, rfs := NewFs()
	t.Cleanup(func() { _ = os.RemoveAll(rfs.root) })

	require.NoError(t, rfs.CreateServerFile("a", randomData(t, 4096)))

	// Hold the lookup lock so that the caller sees a stale cache and waits.
	fs.lookupMu.Lock()
	done := make(chan error, 1)
	go func() {
		_, err := fs.DiskUsage(false)
		done <- err
	}()
	time.Sleep(100 * time.Millisecond)

	// While the caller waits, another walk finishes and refreshes the cache.
	fs.lastLookupTime.Set(time.Now())
	fs.unixFS.SetUsage(12345)
	fs.lookupMu.Unlock()

	require.NoError(t, <-done)
	assert.Equal(t, int64(12345), fs.CachedUsage())
}
