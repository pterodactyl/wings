package filesystem

import (
	"archive/tar"
	"emperror.dev/errors"
	"github.com/apex/log"
	"github.com/juju/ratelimit"
	"github.com/karrick/godirwalk"
	"github.com/klauspost/pgzip"
	"github.com/pterodactyl/wings/config"
	"github.com/sabhiram/go-gitignore"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

const memory = 4 * 1024

var pool = sync.Pool{
	New: func() interface{} {
		b := make([]byte, memory)
		return b
	},
}

type Archive struct {
	// BasePath is the absolute path to create the archive from where Files and Ignore are
	// relative to.
	BasePath string

	// Ignore is a gitignore string (most likely read from a file) of files to ignore
	// from the archive.
	Ignore string

	// Files specifies the files to archive, this takes priority over the Ignore option, if
	// unspecified, all files in the BasePath will be archived unless Ignore is set.
	Files []string
}

// Creates an archive at dst with all of the files defined in the included files struct.
func (a *Archive) Create(dst string) error {
	f, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	defer f.Close()

	// Select a writer based off of the WriteLimit configuration option.
	var writer io.Writer
	if writeLimit := config.Get().System.Backups.WriteLimit; writeLimit < 1 {
		// If there is no write limit, use the file as the writer.
		writer = f
	} else {
		// Token bucket with a capacity of "writeLimit" MiB, adding "writeLimit" MiB/s
		bucket := ratelimit.NewBucketWithRate(
			float64(writeLimit)*1024*1024,
			int64(writeLimit)*1024*1024,
		)

		// Wrap the file writer with the token bucket limiter.
		writer = ratelimit.Writer(f, bucket)
	}

	// Create a new gzip writer around the file.
	gw, _ := pgzip.NewWriterLevel(writer, pgzip.BestSpeed)
	_ = gw.SetConcurrency(1<<20, 1)
	defer gw.Close()

	// Create a new tar writer around the gzip writer.
	tw := tar.NewWriter(gw)
	defer tw.Close()

	// Configure godirwalk.
	options := &godirwalk.Options{
		FollowSymbolicLinks: false,
		Unsorted:            true,
	}

	// Selectively pick the godirwalk callback based off of the options of the archiver.
	if len(a.Files) > 0 {
		options.Callback = a.filesCallback(tw)
	} else if len(a.Ignore) > 0 {
		i := ignore.CompileIgnoreLines(strings.Split(a.Ignore, "\n")...)
		options.Callback = a.ignoreCallback(tw, i)
	} else {
		options.Callback = a.callback(tw)
	}

	// Recursively walk the path we are archiving.
	if err := godirwalk.Walk(a.BasePath, options); err != nil {
		return err
	}

	return nil
}

func (a *Archive) callback(tw *tar.Writer) func(path string, de *godirwalk.Dirent) error {
	return func(path string, de *godirwalk.Dirent) error {
		// Skip directories because we walking them recursively.
		if de.IsDir() {
			return nil
		}

		relativePath := filepath.ToSlash(
			strings.TrimPrefix(
				path,
				a.BasePath+string(filepath.Separator),
			),
		)

		// Add the file to the archive, if it is nested in a directory,
		// the directory will be automatically "created" in the archive.
		return a.addToArchive(path, relativePath, tw)
	}
}

func (a *Archive) ignoreCallback(tw *tar.Writer, i *ignore.GitIgnore) func(path string, de *godirwalk.Dirent) error {
	return func(path string, de *godirwalk.Dirent) error {
		// Skip directories because we walking them recursively.
		if de.IsDir() {
			return nil
		}

		relativePath := filepath.ToSlash(
			strings.TrimPrefix(
				path,
				a.BasePath+string(filepath.Separator),
			),
		)
		if i.MatchesPath(relativePath) {
			return godirwalk.SkipThis
		}

		// Add the file to the archive, if it is nested in a directory,
		// the directory will be automatically "created" in the archive.
		return a.addToArchive(path, relativePath, tw)
	}
}

func (a *Archive) filesCallback(tw *tar.Writer) func(path string, de *godirwalk.Dirent) error {
	log.Debug(strings.Join(a.Files, ","))
	return func(path string, de *godirwalk.Dirent) error {
		// Skip directories because we walking them recursively.
		if de.IsDir() {
			return nil
		}

		log.Debug(path)
		for _, f := range a.Files {
			if path != f && !strings.HasPrefix(path, f) {
				continue
			}

			relativePath := filepath.ToSlash(
				strings.TrimPrefix(
					path,
					a.BasePath+string(filepath.Separator),
				),
			)

			// Add the file to the archive, if it is nested in a directory,
			// the directory will be automatically "created" in the archive.
			return a.addToArchive(path, relativePath, tw)
		}

		return godirwalk.SkipThis
	}
}

func (a *Archive) addToArchive(p string, rp string, w *tar.Writer) error {
	// Lstat the file, this will give us the same information as Stat except
	// that it will not follow a symlink to it's target automatically.
	s, err := os.Lstat(p)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return errors.WithMessage(err, "failed to Lstat '"+rp+"'")
	}

	// Resolve the symlink target if the file is a symlink.
	var target string
	if s.Mode()&os.ModeSymlink != 0 {
		// Read the target of the symlink.
		target, err = os.Readlink(s.Name())
		if err != nil {
			return errors.WithMessage(err, "failed to read symlink target for '"+rp+"'")
		}

		// Normalize slashes in the target path.
		//
		// This is only really-required when running on OSes that don't use `/`.
		target = filepath.ToSlash(target)
	}

	// Get the tar FileInfoHeader in order to add the file to the archive.
	header, err := tar.FileInfoHeader(s, target)
	if err != nil {
		return errors.WithMessage(err, "failed to get tar#FileInfoHeader for '"+rp+"'")
	}

	// Fix the header name if the file is not a symlink.
	if s.Mode()&os.ModeSymlink == 0 {
		header.Name = rp
	}

	// Write the tar FileInfoHeader to the archive.
	if err := w.WriteHeader(header); err != nil {
		return errors.WithMessage(err, "failed to write tar#FileInfoHeader for '"+rp+"'")
	}

	// If the size of the file is less than 1 (most likely for symlinks), skip writing the file.
	if header.Size < 1 {
		return nil
	}

	// If the buffer size is larger than the file size, create a smaller buffer to hold the file.
	var buf []byte
	if header.Size < memory {
		buf = make([]byte, header.Size)
	} else {
		// Get a fixed-size buffer from the pool to save on allocations.
		buf = pool.Get().([]byte)
		defer func() {
			buf = make([]byte, memory)
			pool.Put(buf)
		}()
	}

	// Open the file.
	f, err := os.Open(p)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return errors.WithMessage(err, "failed to open '"+header.Name+"' for copying")
	}
	defer f.Close()

	// Copy the file's contents to the archive using our buffer.
	if _, err := io.CopyBuffer(w, io.LimitReader(f, header.Size), buf); err != nil {
		return errors.WithMessage(err, "failed to copy '"+header.Name+"' to archive")
	}

	return nil
}
