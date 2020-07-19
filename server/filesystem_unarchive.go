package server

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"fmt"
	"github.com/mholt/archiver/v3"
	"github.com/pkg/errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
)

// Look through a given archive and determine if decompressing it would put the server over
// its allocated disk space limit.
func (fs *Filesystem) SpaceAvailableForDecompression(dir string, file string) (bool, error) {
	// Don't waste time trying to determine this if we know the server will have the space for
	// it since there is no limit.
	if fs.Server.Build().DiskSpace <= 0 {
		return true, nil
	}

	source, err := fs.SafePath(filepath.Join(dir, file))
	if err != nil {
		return false, err
	}

	wg := new(sync.WaitGroup)

	var dirSize int64
	var cErr error
	// Get the cached size in a parallel process so that if it is not cached we are not
	// waiting an unnecessary amount of time on this call.
	go func() {
		wg.Add(1)
		defer wg.Done()

		dirSize, cErr = fs.getCachedDiskUsage()
	}()

	var size int64
	// In a seperate thread, walk over the archive and figure out just how large the final
	// output would be from dearchiving it.
	go func() {
		wg.Add(1)
		defer wg.Done()

		// Walk all of the files and calculate the total decompressed size of this archive.
		archiver.Walk(source, func(f archiver.File) error {
			atomic.AddInt64(&size, f.Size())

			return nil
		})
	}()

	wg.Wait()

	return ((dirSize + size) / 1000.0 / 1000.0) <= fs.Server.Build().DiskSpace, cErr
}

// Decompress a file in a given directory by using the archiver tool to infer the file
// type and go from there. This will walk over all of the files within the given archive
// and ensure that there is not a zip-slip attack being attempted by validating that the
// final path is within the server data directory.
func (fs *Filesystem) DecompressFile(dir string, file string) error {
	source, err := fs.SafePath(filepath.Join(dir, file))
	if err != nil {
		return errors.WithStack(err)
	}

	// Walk over all of the files spinning up an additional go-routine for each file we've encountered
	// and then extract that file from the archive and write it to the disk. If any part of this process
	// encounters an error the entire process will be stopped.
	return archiver.Walk(source, func(f archiver.File) error {
		// Don't waste time with directories, we don't need to create them if they have no contents, and
		// we will ensure the directory exists when opening the file for writing anyways.
		if f.IsDir() {
			return nil
		}

		return fs.extractFileFromArchive(f)
	})
}

// Extracts a single file from the archive and writes it to the disk after verifying that it will end
// up in the server data directory.
func (fs *Filesystem) extractFileFromArchive(f archiver.File) error {
	var name string

	switch s := f.Sys().(type) {
	case *tar.Header:
		name = s.Name
	case *gzip.Header:
		name = s.Name
	case *zip.FileHeader:
		name = s.Name
	default:
		return errors.New(fmt.Sprintf("could not parse underlying data source with type %s", reflect.TypeOf(s).String()))
	}

	// Guard against a zip-slip attack and prevent writing a file to a destination outside of
	// the server root directory.
	p, err := fs.SafePath(name)
	if err != nil {
		return err
	}

	// Ensure the directory structure for this file exists before trying to write the file
	// to the disk, otherwise we'll have some unexpected fun.
	if err := os.MkdirAll(strings.TrimSuffix(p, filepath.Base(p)), 0755); err != nil {
		return err
	}

	// Open the file and truncate it if it already exists.
	o, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return err
	}

	defer o.Close()

	_, cerr := io.Copy(o, f)

	return cerr
}
