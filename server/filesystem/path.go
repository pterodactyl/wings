package filesystem

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"emperror.dev/errors"
	"golang.org/x/sync/errgroup"
)

// Checks if the given file or path is in the server's file denylist. If so, an Error
// is returned, otherwise nil is returned.
func (fs *Filesystem) IsIgnored(paths ...string) error {
	for _, p := range paths {
		sp, err := fs.SafePath(p)
		if err != nil {
			return err
		}
		if fs.denylist.MatchesPath(sp) {
			return errors.WithStack(&Error{code: ErrCodeDenylistFile, path: p, resolved: sp})
		}
	}
	return nil
}

// Normalizes a directory being passed in to ensure the user is not able to escape
// from their data directory. After normalization if the directory is still within their home
// path it is returned. If they managed to "escape" an error will be returned.
//
// This logic is actually copied over from the SFTP server code. Ideally that eventually
// either gets ported into this application, or is able to make use of this package.
func (fs *Filesystem) SafePath(p string) (string, error) {
	var nonExistentPathResolution string

	// Start with a cleaned up path before checking the more complex bits.
	r := fs.unsafeFilePath(p)

	// At the same time, evaluate the symlink status and determine where this file or folder
	// is truly pointing to.
	ep, err := filepath.EvalSymlinks(r)
	if err != nil && !os.IsNotExist(err) {
		return "", errors.Wrap(err, "server/filesystem: failed to evaluate symlink")
	} else if os.IsNotExist(err) {
		// The requested directory doesn't exist, so at this point we need to iterate up the
		// path chain until we hit a directory that _does_ exist and can be validated.
		parts := strings.Split(filepath.Dir(r), "/")

		var try string
		// Range over all of the path parts and form directory pathings from the end
		// moving up until we have a valid resolution or we run out of paths to try.
		for k := range parts {
			try = strings.Join(parts[:(len(parts)-k)], "/")

			if !fs.unsafeIsInDataDirectory(try) {
				break
			}

			t, err := filepath.EvalSymlinks(try)
			if err == nil {
				nonExistentPathResolution = t
				break
			}
		}
	}

	// If the new path doesn't start with their root directory there is clearly an escape
	// attempt going on, and we should NOT resolve this path for them.
	if nonExistentPathResolution != "" {
		if !fs.unsafeIsInDataDirectory(nonExistentPathResolution) {
			return "", NewBadPathResolution(p, nonExistentPathResolution)
		}

		// If the nonExistentPathResolution variable is not empty then the initial path requested
		// did not exist and we looped through the pathway until we found a match. At this point
		// we've confirmed the first matched pathway exists in the root server directory, so we
		// can go ahead and just return the path that was requested initially.
		return r, nil
	}

	// If the requested directory from EvalSymlinks begins with the server root directory go
	// ahead and return it. If not we'll return an error which will block any further action
	// on the file.
	if fs.unsafeIsInDataDirectory(ep) {
		return ep, nil
	}

	return "", NewBadPathResolution(p, r)
}

// Generate a path to the file by cleaning it up and appending the root server path to it. This
// DOES NOT guarantee that the file resolves within the server data directory. You'll want to use
// the fs.unsafeIsInDataDirectory(p) function to confirm.
func (fs *Filesystem) unsafeFilePath(p string) string {
	// Calling filepath.Clean on the joined directory will resolve it to the absolute path,
	// removing any ../ type of resolution arguments, and leaving us with a direct path link.
	//
	// This will also trim the existing root path off the beginning of the path passed to
	// the function since that can get a bit messy.
	return filepath.Clean(filepath.Join(fs.Path(), strings.TrimPrefix(p, fs.Path())))
}

// Check that that path string starts with the server data directory path. This function DOES NOT
// validate that the rest of the path does not end up resolving out of this directory, or that the
// targeted file or folder is not a symlink doing the same thing.
func (fs *Filesystem) unsafeIsInDataDirectory(p string) bool {
	return strings.HasPrefix(strings.TrimSuffix(p, "/")+"/", strings.TrimSuffix(fs.Path(), "/")+"/")
}

// Executes the fs.SafePath function in parallel against an array of paths. If any of the calls
// fails an error will be returned.
func (fs *Filesystem) ParallelSafePath(paths []string) ([]string, error) {
	var cleaned []string

	// Simple locker function to avoid racy appends to the array of cleaned paths.
	m := new(sync.Mutex)
	push := func(c string) {
		m.Lock()
		cleaned = append(cleaned, c)
		m.Unlock()
	}

	// Create an error group that we can use to run processes in parallel while retaining
	// the ability to cancel the entire process immediately should any of it fail.
	g, ctx := errgroup.WithContext(context.Background())

	// Iterate over all of the paths and generate a cleaned path, if there is an error for any
	// of the files, abort the process.
	for _, p := range paths {
		// Create copy so we can use it within the goroutine correctly.
		pi := p

		// Recursively call this function to continue digging through the directory tree within
		// a separate goroutine. If the context is canceled abort this process.
		g.Go(func() error {
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
				// If the callback returns true, go ahead and keep walking deeper. This allows
				// us to programmatically continue deeper into directories, or stop digging
				// if that pathway knows it needs nothing else.
				if c, err := fs.SafePath(pi); err != nil {
					return err
				} else {
					push(c)
				}

				return nil
			}
		})
	}

	// Block until all of the routines finish and have returned a value.
	return cleaned, g.Wait()
}
