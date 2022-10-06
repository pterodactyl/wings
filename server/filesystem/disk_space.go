package filesystem

import (
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"emperror.dev/errors"
	"github.com/apex/log"
	"github.com/karrick/godirwalk"
)

type SpaceCheckingOpts struct {
	AllowStaleResponse bool
}

type usageLookupTime struct {
	sync.RWMutex
	value time.Time
}

// Update the last time that a disk space lookup was performed.
func (ult *usageLookupTime) Set(t time.Time) {
	ult.Lock()
	ult.value = t
	ult.Unlock()
}

// Get the last time that we performed a disk space usage lookup.
func (ult *usageLookupTime) Get() time.Time {
	ult.RLock()
	defer ult.RUnlock()

	return ult.value
}

// Returns the maximum amount of disk space that this Filesystem instance is allowed to use.
func (fs *Filesystem) MaxDisk() int64 {
	return atomic.LoadInt64(&fs.diskLimit)
}

// Sets the disk space limit for this Filesystem instance.
func (fs *Filesystem) SetDiskLimit(i int64) {
	atomic.SwapInt64(&fs.diskLimit, i)
}

// The same concept as HasSpaceAvailable however this will return an error if there is
// no space, rather than a boolean value.
func (fs *Filesystem) HasSpaceErr(allowStaleValue bool) error {
	if !fs.HasSpaceAvailable(allowStaleValue) {
		return newFilesystemError(ErrCodeDiskSpace, nil)
	}
	return nil
}

// Determines if the directory a file is trying to be added to has enough space available
// for the file to be written to.
//
// Because determining the amount of space being used by a server is a taxing operation we
// will load it all up into a cache and pull from that as long as the key is not expired.
//
// This operation will potentially block unless allowStaleValue is set to true. See the
// documentation on DiskUsage for how this affects the call.
func (fs *Filesystem) HasSpaceAvailable(allowStaleValue bool) bool {
	size, err := fs.DiskUsage(allowStaleValue)
	if err != nil {
		log.WithField("root", fs.root).WithField("error", err).Warn("failed to determine root fs directory size")
	}

	// If space is -1 or 0 just return true, means they're allowed unlimited.
	//
	// Technically we could skip disk space calculation because we don't need to check if the
	// server exceeds its limit but because this method caches the disk usage it would be best
	// to calculate the disk usage and always return true.
	if fs.MaxDisk() == 0 {
		return true
	}

	return size <= fs.MaxDisk()
}

// Returns the cached value for the amount of disk space used by the filesystem. Do not rely on this
// function for critical logical checks. It should only be used in areas where the actual disk usage
// does not need to be perfect, e.g. API responses for server resource usage.
func (fs *Filesystem) CachedUsage() int64 {
	return atomic.LoadInt64(&fs.diskUsed)
}

// Internal helper function to allow other parts of the codebase to check the total used disk space
// as needed without overly taxing the system. This will prioritize the value from the cache to avoid
// excessive IO usage. We will only walk the filesystem and determine the size of the directory if there
// is no longer a cached value.
//
// If "allowStaleValue" is set to true, a stale value MAY be returned to the caller if there is an
// expired cache value AND there is currently another lookup in progress. If there is no cached value but
// no other lookup is in progress, a fresh disk space response will be returned to the caller.
//
// This is primarily to avoid a bunch of I/O operations from piling up on the server, especially on servers
// with a large amount of files.
func (fs *Filesystem) DiskUsage(allowStaleValue bool) (int64, error) {
	// A disk check interval of 0 means this functionality is completely disabled.
	if fs.diskCheckInterval == 0 {
		return 0, nil
	}

	if !fs.lastLookupTime.Get().After(time.Now().Add(time.Second * fs.diskCheckInterval * -1)) {
		// If we are now allowing a stale response go ahead  and perform the lookup and return the fresh
		// value. This is a blocking operation to the calling process.
		if !allowStaleValue {
			return fs.updateCachedDiskUsage()
		} else if !fs.lookupInProgress.Load() {
			// Otherwise, if we allow a stale value and there isn't a valid item in the cache and we aren't
			// currently performing a lookup, just do the disk usage calculation in the background.
			go func(fs *Filesystem) {
				if _, err := fs.updateCachedDiskUsage(); err != nil {
					log.WithField("root", fs.root).WithField("error", err).Warn("failed to update fs disk usage from within routine")
				}
			}(fs)
		}
	}

	// Return the currently cached value back to the calling function.
	return atomic.LoadInt64(&fs.diskUsed), nil
}

// Updates the currently used disk space for a server.
func (fs *Filesystem) updateCachedDiskUsage() (int64, error) {
	// Obtain an exclusive lock on this process so that we don't unintentionally run it at the same
	// time as another running process. Once the lock is available it'll read from the cache for the
	// second call rather than hitting the disk in parallel.
	fs.mu.Lock()
	defer fs.mu.Unlock()

	// Signal that we're currently updating the disk size so that other calls to the disk checking
	// functions can determine if they should queue up additional calls to this function. Ensure that
	// we always set this back to "false" when this process is done executing.
	fs.lookupInProgress.Store(true)
	defer fs.lookupInProgress.Store(false)

	// If there is no size its either because there is no data (in which case running this function
	// will have effectively no impact), or there is nothing in the cache, in which case we need to
	// grab the size of their data directory. This is a taxing operation, so we want to store it in
	// the cache once we've gotten it.
	size, err := fs.DirectorySize("/")

	// Always cache the size, even if there is an error. We want to always return that value
	// so that we don't cause an endless loop of determining the disk size if there is a temporary
	// error encountered.
	fs.lastLookupTime.Set(time.Now())

	atomic.StoreInt64(&fs.diskUsed, size)

	return size, err
}

// Determines the directory size of a given location by running parallel tasks to iterate
// through all of the folders. Returns the size in bytes. This can be a fairly taxing operation
// on locations with tons of files, so it is recommended that you cache the output.
func (fs *Filesystem) DirectorySize(dir string) (int64, error) {
	d, err := fs.SafePath(dir)
	if err != nil {
		return 0, err
	}

	var size int64
	var st syscall.Stat_t

	err = godirwalk.Walk(d, &godirwalk.Options{
		Unsorted: true,
		Callback: func(p string, e *godirwalk.Dirent) error {
			// If this is a symlink then resolve the final destination of it before trying to continue walking
			// over its contents. If it resolves outside the server data directory just skip everything else for
			// it. Otherwise, allow it to continue.
			if e.IsSymlink() {
				if _, err := fs.SafePath(p); err != nil {
					if IsErrorCode(err, ErrCodePathResolution) {
						return godirwalk.SkipThis
					}

					return err
				}
			}

			if !e.IsDir() {
				syscall.Lstat(p, &st)
				atomic.AddInt64(&size, st.Size)
			}

			return nil
		},
	})

	return size, errors.WrapIf(err, "server/filesystem: directorysize: failed to walk directory")
}

// Helper function to determine if a server has space available for a file of a given size.
// If space is available, no error will be returned, otherwise an ErrNotEnoughSpace error
// will be raised.
func (fs *Filesystem) HasSpaceFor(size int64) error {
	if fs.MaxDisk() == 0 {
		return nil
	}
	s, err := fs.DiskUsage(true)
	if err != nil {
		return err
	}
	if (s + size) > fs.MaxDisk() {
		return newFilesystemError(ErrCodeDiskSpace, nil)
	}
	return nil
}

// Updates the disk usage for the Filesystem instance.
func (fs *Filesystem) addDisk(i int64) int64 {
	size := atomic.LoadInt64(&fs.diskUsed)

	// Sorry go gods. This is ugly but the best approach I can come up with for right
	// now without completely re-evaluating the logic we use for determining disk space.
	//
	// Normally I would just be using the atomic load right below, but I'm not sure about
	// the scenarios where it is 0 because nothing has run that would trigger a disk size
	// calculation?
	//
	// Perhaps that isn't even a concern for the sake of this?
	if !fs.isTest {
		size, _ = fs.DiskUsage(true)
	}

	// If we're dropping below 0 somehow just cap it to 0.
	if (size + i) < 0 {
		return atomic.SwapInt64(&fs.diskUsed, 0)
	}

	return atomic.AddInt64(&fs.diskUsed, i)
}
