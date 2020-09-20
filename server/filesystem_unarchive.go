package server

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"fmt"
	"github.com/mholt/archiver/v3"
	"github.com/pkg/errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
)

var ErrUnknownArchiveFormat = errors.New("filesystem: unknown archive format")

// Look through a given archive and determine if decompressing it would put the server over
// its allocated disk space limit.
func (fs *Filesystem) SpaceAvailableForDecompression(dir string, file string) (bool, error) {
	// Don't waste time trying to determine this if we know the server will have the space for
	// it since there is no limit.
	if fs.Server.DiskSpace() <= 0 {
		return true, nil
	}

	source, err := fs.SafePath(filepath.Join(dir, file))
	if err != nil {
		return false, err
	}

	// Get the cached size in a parallel process so that if it is not cached we are not
	// waiting an unnecessary amount of time on this call.
	dirSize, err := fs.DiskUsage(false)

	var size int64
	var max = fs.Server.DiskSpace() * 1000.0 * 1000.0
	// Walk over the archive and figure out just how large the final output would be from unarchiving it.
	err = archiver.Walk(source, func(f archiver.File) error {
		if atomic.AddInt64(&size, f.Size())+dirSize > max {
			return errors.WithStack(ErrNotEnoughDiskSpace)
		}

		return nil
	})
	if err != nil {
		if strings.HasPrefix(err.Error(), "format ") {
			return false, errors.WithStack(ErrUnknownArchiveFormat)
		}

		return false, errors.WithStack(err)
	}

	return true, errors.WithStack(err)
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

	// Make sure the file exists basically.
	if _, err := os.Stat(source); err != nil {
		return errors.WithStack(err)
	}

	// Walk over all of the files spinning up an additional go-routine for each file we've encountered
	// and then extract that file from the archive and write it to the disk. If any part of this process
	// encounters an error the entire process will be stopped.
	err = archiver.Walk(source, func(f archiver.File) error {
		// Don't waste time with directories, we don't need to create them if they have no contents, and
		// we will ensure the directory exists when opening the file for writing anyways.
		if f.IsDir() {
			return nil
		}

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

		p, err := fs.SafePath(filepath.Join(dir, name))
		if err != nil {
			return errors.Wrap(err, "failed to generate a safe path to server file")
		}

		return errors.Wrap(fs.Writefile(p, f), "could not extract file from archive")
	})
	if err != nil {
		if strings.HasPrefix(err.Error(), "format ") {
			return errors.WithStack(ErrUnknownArchiveFormat)
		}

		return errors.WithStack(err)
	}

	return nil
}
