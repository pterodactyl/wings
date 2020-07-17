package server

import (
	"context"
	"github.com/gammazero/workerpool"
	"io/ioutil"
	"os"
	"path/filepath"
	"runtime"
	"sync"
)

type FileWalker struct {
	*Filesystem
}

type PooledFileWalker struct {
	wg       sync.WaitGroup
	pool     *workerpool.WorkerPool
	callback filepath.WalkFunc
	cancel   context.CancelFunc

	err     error
	errOnce sync.Once

	Filesystem *Filesystem
}

// Returns a new walker instance.
func (fs *Filesystem) NewWalker() *FileWalker {
	return &FileWalker{fs}
}

// Creates a new pooled file walker that will concurrently walk over a given directory but limit itself
// to a worker pool as to not completely flood out the system or cause a process crash.
func newPooledWalker(fs *Filesystem) *PooledFileWalker {
	return &PooledFileWalker{
		Filesystem: fs,
		// Create a worker pool that is the same size as the number of processors available on the
		// system. Going much higher doesn't provide much of a performance boost, and is only more
		// likely to lead to resource overloading anyways.
		pool: workerpool.New(runtime.GOMAXPROCS(0)),
	}
}

// Process a given path by calling the callback function for all of the files and directories within
// the path, and then dropping into any directories that we come across.
func (w *PooledFileWalker) process(path string) error {
	p, err := w.Filesystem.SafePath(path)
	if err != nil {
		return err
	}

	files, err := ioutil.ReadDir(p)
	if err != nil {
		return err
	}

	// Loop over all of the files and directories in the given directory and call the provided
	// callback function. If we encounter a directory, push that directory onto the worker queue
	// to be processed.
	for _, f := range files {
		sp := filepath.Join(p, f.Name())
		i, err := os.Stat(sp)

		// Call the user-provided callback for this file or directory. If an error is returned that is
		// not a SkipDir call, abort the entire process and bubble that error up.
		if err = w.callback(sp, i, err); err != nil && err != filepath.SkipDir {
			return err
		}

		// If this is a directory, and we didn't get a SkipDir error, continue through by pushing another
		// job to the pool to handle it. If we requested a skip, don't do anything just continue on to the
		// next item.
		if i.IsDir() && err != filepath.SkipDir {
			w.push(sp)
		} else if !i.IsDir() && err == filepath.SkipDir {
			// Per the spec for the callback, if we get a SkipDir error but it is returned for an item
			// that is _not_ a directory, abort the remaining operations on the directory.
			return nil
		}
	}

	return nil
}

// Push a new path into the worker pool and increment the waitgroup so that we do not return too
// early and cause panic's as internal directories attempt to submit to the pool.
func (w *PooledFileWalker) push(path string) {
	w.wg.Add(1)
	w.pool.Submit(func() {
		defer w.wg.Done()
		if err := w.process(path); err != nil {
			w.errOnce.Do(func() {
				w.err = err
				if w.cancel != nil {
					w.cancel()
				}
			})
		}
	})
}

// Walks the given directory and executes the callback function for all of the files and directories
// that are encountered.
func (fs *Filesystem) Walk(dir string, callback filepath.WalkFunc) error {
	w := newPooledWalker(fs)
	w.callback = callback

	_, cancel := context.WithCancel(context.Background())
	w.cancel = cancel

	w.push(dir)

	w.wg.Wait()
	w.pool.StopWait()

	if w.err != nil {
		return w.err
	}

	return nil
}
