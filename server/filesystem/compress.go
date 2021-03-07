package filesystem

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/mholt/archiver/v3"
	"github.com/pterodactyl/wings/system"
)

// CompressFiles compresses all of the files matching the given paths in the
// specified directory. This function also supports passing nested paths to only
// compress certain files and folders when working in a larger directory. This
// effectively creates a local backup, but rather than ignoring specific files
// and folders, it takes an allow-list of files and folders.
//
// All paths are relative to the dir that is passed in as the first argument,
// and the compressed file will be placed at that location named
// `archive-{date}.tar.gz`.
func (fs *Filesystem) CompressFiles(dir string, paths []string) (os.FileInfo, error) {
	cleanedRootDir, err := fs.SafePath(dir)
	if err != nil {
		return nil, err
	}

	// Take all of the paths passed in and merge them together with the root directory we've gotten.
	for i, p := range paths {
		paths[i] = filepath.Join(cleanedRootDir, p)
	}

	cleaned, err := fs.ParallelSafePath(paths)
	if err != nil {
		return nil, err
	}

	a := &Archive{BasePath: cleanedRootDir, Files: cleaned}
	d := path.Join(
		cleanedRootDir,
		fmt.Sprintf("archive-%s.tar.gz", strings.ReplaceAll(time.Now().Format(time.RFC3339), ":", "")),
	)

	if err := a.Create(d); err != nil {
		return nil, err
	}

	f, err := os.Stat(d)
	if err != nil {
		_ = os.Remove(d)
		return nil, err
	}

	if err := fs.HasSpaceFor(f.Size()); err != nil {
		_ = os.Remove(d)
		return nil, err
	}

	fs.addDisk(f.Size())

	return f, nil
}

// SpaceAvailableForDecompression looks through a given archive and determines
// if decompressing it would put the server over its allocated disk space limit.
func (fs *Filesystem) SpaceAvailableForDecompression(dir string, file string) error {
	// Don't waste time trying to determine this if we know the server will have the space for
	// it since there is no limit.
	if fs.MaxDisk() <= 0 {
		return nil
	}

	source, err := fs.SafePath(filepath.Join(dir, file))
	if err != nil {
		return err
	}

	// Get the cached size in a parallel process so that if it is not cached we are not
	// waiting an unnecessary amount of time on this call.
	dirSize, err := fs.DiskUsage(false)

	var size int64
	// Walk over the archive and figure out just how large the final output would be from unarchiving it.
	err = archiver.Walk(source, func(f archiver.File) error {
		if atomic.AddInt64(&size, f.Size())+dirSize > fs.MaxDisk() {
			return &Error{code: ErrCodeDiskSpace}
		}
		return nil
	})
	if err != nil {
		if strings.HasPrefix(err.Error(), "format ") {
			return &Error{code: ErrCodeUnknownArchive}
		}
		return err
	}
	return err
}

// DecompressFile will decompress a file in a given directory by using the
// archiver tool to infer the file type and go from there. This will walk over
// all of the files within the given archive and ensure that there is not a
// zip-slip attack being attempted by validating that the final path is within
// the server data directory.
func (fs *Filesystem) DecompressFile(dir string, file string) error {
	source, err := fs.SafePath(filepath.Join(dir, file))
	if err != nil {
		return err
	}
	// Ensure that the source archive actually exists on the system.
	if _, err := os.Stat(source); err != nil {
		return err
	}

	// Walk all of the files in the archiver file and write them to the disk. If any
	// directory is encountered it will be skipped since we handle creating any missing
	// directories automatically when writing files.
	err = archiver.Walk(source, func(f archiver.File) error {
		if f.IsDir() {
			return nil
		}
		name, err := system.ExtractArchiveSourceName(f, dir)
		if err != nil {
			return WrapError(err, filepath.Join(dir, f.Name()))
		}
		p := filepath.Join(dir, name)
		// If it is ignored, just don't do anything with the file and skip over it.
		if err := fs.IsIgnored(p); err != nil {
			return nil
		}
		if err := fs.Writefile(p, f); err != nil {
			return &Error{code: ErrCodeUnknownError, err: err, resolved: source}
		}
		return nil
	})
	if err != nil {
		if strings.HasPrefix(err.Error(), "format ") {
			return &Error{code: ErrCodeUnknownArchive}
		}
		return err
	}
	return nil
}
