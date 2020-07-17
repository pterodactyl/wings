package server

import (
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
	defer w.wg.Done()

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

		if err = w.callback(sp, i, err); err != nil {
			if err == filepath.SkipDir {
				return nil
			}

			return err
		}

		if i.IsDir() {
			w.push(sp)
		}
	}

	return nil
}

// Push a new path into the worker pool.
//
// @todo probably helps to handle errors.
func (w *PooledFileWalker) push(path string) {
	w.wg.Add(1)
	w.pool.Submit(func() {
		w.process(path)
	})
}

// Walks the given directory and executes the callback function for all of the files and directories
// that are encountered.
func (fs *Filesystem) Walk(dir string, callback filepath.WalkFunc) error {
	w := newPooledWalker(fs)
	w.callback = callback

	w.push(dir)

	w.wg.Wait()
	w.pool.StopWait()

	return nil
}
