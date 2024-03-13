package filesystem

import (
	"sync"
	"sync/atomic"
	"time"

	"emperror.dev/errors"
	"github.com/apex/log"

	"github.com/pterodactyl/wings/internal/ufs"
)

type SpaceCheckingOpts struct {
	AllowStaleResponse bool
}

// TODO: can this be replaced with some sort of atomic? Like atomic.Pointer?
type usageLookupTime struct {
	sync.RWMutex
	value time.Time
}

// Set sets the last time that a disk space lookup was performed.
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

// MaxDisk returns the maximum amount of disk space that this Filesystem
// instance is allowed to use.
func (fs *Filesystem) MaxDisk() int64 {
	return fs.unixFS.Limit()
}

// SetDiskLimit sets the disk space limit for this Filesystem instance.
func (fs *Filesystem) SetDiskLimit(i int64) {
	fs.unixFS.SetLimit(i)
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
		log.WithField("root", fs.Path()).WithField("error", err).Warn("failed to determine root fs directory size")
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
	return fs.unixFS.Usage()
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
					log.WithField("root", fs.Path()).WithField("error", err).Warn("failed to update fs disk usage from within routine")
				}
			}(fs)
		}
	}

	// Return the currently cached value back to the calling function.
	return fs.unixFS.Usage(), nil
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

	fs.unixFS.SetUsage(size)

	return size, err
}

// DirectorySize calculates the size of a directory and its descendants.
func (fs *Filesystem) DirectorySize(root string) (int64, error) {
	dirfd, name, closeFd, err := fs.unixFS.SafePath(root)
	defer closeFd()
	if err != nil {
		return 0, err
	}

	var size atomic.Int64
	err = fs.unixFS.WalkDirat(dirfd, name, func(dirfd int, name, _ string, d ufs.DirEntry, err error) error {
		if err != nil {
			return errors.Wrap(err, "walkdirat err")
		}

		// Only calculate the size of regular files.
		if !d.Type().IsRegular() {
			return nil
		}

		info, err := fs.unixFS.Lstatat(dirfd, name)
		if err != nil {
			return errors.Wrap(err, "lstatat err")
		}

		// TODO: detect if info is a hard-link and de-duplicate it.
		// ref; https://github.com/pterodactyl/wings/pull/181/files

		size.Add(info.Size())
		return nil
	})
	return size.Load(), errors.WrapIf(err, "server/filesystem: directorysize: failed to walk directory")
}

func (fs *Filesystem) HasSpaceFor(size int64) error {
	if !fs.unixFS.CanFit(size) {
		return newFilesystemError(ErrCodeDiskSpace, nil)
	}
	return nil
}

// Updates the disk usage for the Filesystem instance.
func (fs *Filesystem) addDisk(i int64) int64 {
	return fs.unixFS.Add(i)
}
