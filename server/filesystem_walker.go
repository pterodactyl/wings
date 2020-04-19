package server

import (
	"context"
	"golang.org/x/sync/errgroup"
	"io/ioutil"
	"os"
	"path/filepath"
)

type FileWalker struct {
	*Filesystem
}

// Returns a new walker instance.
func (fs *Filesystem) NewWalker() *FileWalker {
	return &FileWalker{fs}
}

// Iterate over all of the files and directories within a given directory. When a file is
// found the callback will be called with the file information. If a directory is encountered
// it will be recursively passed back through to this function.
func (fw *FileWalker) Walk(dir string, ctx context.Context, callback func (os.FileInfo) bool) error {
	cleaned, err := fw.SafePath(dir)
	if err != nil {
		return err
	}

	// Get all of the files from this directory.
	files, err := ioutil.ReadDir(cleaned)
	if err != nil {
		return err
	}

	// Create an error group that we can use to run processes in parallel while retaining
	// the ability to cancel the entire process immediately should any of it fail.
	g, ctx := errgroup.WithContext(ctx)

	for _, f := range files {
		if f.IsDir() {
			p := filepath.Join(dir, f.Name())
			// Recursively call this function to continue digging through the directory tree within
			// a seperate goroutine. If the context is canceled abort this process.
			g.Go(func() error {
				select {
				case <-ctx.Done():
					return ctx.Err()
				default:
					// If the callback returns true, go ahead and keep walking deeper. This allows
					// us to programatically continue deeper into directories, or stop digging
					// if that pathway knows it needs nothing else.
					if callback(f) {
						return fw.Walk(p, ctx, callback)
					}

					return nil
				}
			})
		} else {
			// If this isn't a directory, go ahead and pass the file information into the
			// callback. We don't care about the response since we won't be stepping into
			// anything from here.
			callback(f)
		}
	}

	// Block until all of the routines finish and have returned a value.
	return g.Wait()
}