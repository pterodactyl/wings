package filesystem

import (
	"archive/tar"
	"context"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"emperror.dev/errors"
	"github.com/apex/log"
	"github.com/juju/ratelimit"
	"github.com/klauspost/pgzip"
	ignore "github.com/sabhiram/go-gitignore"

	"github.com/pterodactyl/wings/config"
	"github.com/pterodactyl/wings/internal/progress"
	"github.com/pterodactyl/wings/internal/ufs"
)

const memory = 4 * 1024

var pool = sync.Pool{
	New: func() interface{} {
		b := make([]byte, memory)
		return b
	},
}

// SkipThis is used as a return value to indicate that a file should be skipped.
var SkipThis = errors.New("skip this file")

// TarProgress .
type TarProgress struct {
	*tar.Writer
	p *progress.Progress
}

// NewTarProgress .
func NewTarProgress(w *tar.Writer, p *progress.Progress) *TarProgress {
	if p != nil {
		p.Writer = w
	}
	return &TarProgress{
		Writer: w,
		p:      p,
	}
}

// Write .
func (p *TarProgress) Write(v []byte) (int, error) {
	if p.p == nil {
		return p.Writer.Write(v)
	}
	return p.p.Write(v)
}

// Archive represents the original tar.gz archive used for transfers
type Archive struct {
	// Filesystem to create the archive with.
	Filesystem *Filesystem

	// Ignore is a gitignore string (most likely read from a file) of files to ignore
	// from the archive.
	Ignore string

	// BaseDirectory .
	BaseDirectory string

	// Files specifies the files to archive, this takes priority over the Ignore
	// option, if unspecified, all files in the BaseDirectory will be archived
	// unless Ignore is set.
	Files []string

	// Progress wraps the writer of the archive to pass through the progress tracker.
	Progress *progress.Progress

	w *TarProgress
}

// Create creates an archive at dst with all the files defined in the
// included Files array.
//
// THIS IS UNSAFE TO USE IF `dst` IS PROVIDED BY A USER! ONLY USE THIS WITH
// CONTROLLED PATHS!
func (a *Archive) Create(ctx context.Context, dst string) error {
	// Using os.OpenFile here is expected, as long as `dst` is not a user
	// provided path.
	f, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()

	// Select a writer based off of the WriteLimit configuration option. If there is no
	// write limit, use the file as the writer.
	var writer io.Writer
	if writeLimit := int64(config.Get().System.Backups.WriteLimit * 1024 * 1024); writeLimit > 0 {
		// Token bucket with a capacity of "writeLimit" MiB, adding "writeLimit" MiB/s
		// and then wrap the file writer with the token bucket limiter.
		writer = ratelimit.Writer(f, ratelimit.NewBucketWithRate(float64(writeLimit), writeLimit))
	} else {
		writer = f
	}

	return a.Stream(ctx, writer)
}

// Stream streams the creation of the archive to the given writer.
func (a *Archive) Stream(ctx context.Context, w io.Writer) error {
	if a.Filesystem == nil {
		return errors.New("filesystem: archive.Filesystem is unset")
	}

	// The base directory may come with a prefixed `/`, strip it to prevent
	// problems.
	a.BaseDirectory = strings.TrimPrefix(a.BaseDirectory, "/")

	if filesLen := len(a.Files); filesLen > 0 {
		files := make([]string, filesLen)
		for i, f := range a.Files {
			if !strings.HasPrefix(f, a.Filesystem.Path()) {
				files[i] = f
				continue
			}
			files[i] = strings.TrimPrefix(strings.TrimPrefix(f, a.Filesystem.Path()), "/")
		}
		a.Files = files
	}

	// Choose which compression level to use based on the compression_level configuration option
	var compressionLevel int
	switch config.Get().System.Backups.CompressionLevel {
	case "none":
		compressionLevel = pgzip.NoCompression
	case "best_compression":
		compressionLevel = pgzip.BestCompression
	default:
		compressionLevel = pgzip.BestSpeed
	}

	// Create a new gzip writer around the file.
	gw, _ := pgzip.NewWriterLevel(w, compressionLevel)
	_ = gw.SetConcurrency(1<<20, 1)
	defer gw.Close()

	// Create a new tar writer around the gzip writer.
	tw := tar.NewWriter(gw)
	defer tw.Close()

	a.w = NewTarProgress(tw, a.Progress)

	fs := a.Filesystem.unixFS

	// Use WalkDir to walk the filesystem
	baseDir := a.BaseDirectory
	if baseDir == "" {
		baseDir = "."
	}

	// Create a callback function like the legacy version
	callback := a.createCallback()

	return fs.WalkDir(baseDir, func(path string, d ufs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// Calculate relative path - use the path directly since we're walking from root
		relative := path

		// Skip the root directory itself
		if relative == "." {
			return nil
		}

		return callback(relative, d)
	})
}

// createCallback creates a callback function similar to the legacy version
func (a *Archive) createCallback() func(relative string, d ufs.DirEntry) error {
	// Create the appropriate filter function
	var shouldInclude func(relative string) bool

	if len(a.Files) == 0 && len(a.Ignore) > 0 {
		// Use ignore patterns
		ignoreMatcher := ignore.CompileIgnoreLines(strings.Split(a.Ignore, "\n")...)
		shouldInclude = func(relative string) bool {
			return !ignoreMatcher.MatchesPath(relative)
		}
	} else if len(a.Files) > 0 {
		// Use specific file list - exactly like legacy
		shouldInclude = func(relative string) bool {
			for _, f := range a.Files {
				// Exact match or file is within the directory
				if f == relative || strings.HasPrefix(strings.TrimSuffix(relative, "/")+"/", strings.TrimSuffix(f, "/")+"/") {
					return true
				}
			}
			return false
		}
	} else {
		// Include everything
		shouldInclude = func(relative string) bool {
			return true
		}
	}

	return func(relative string, d ufs.DirEntry) error {
		// Skip directories - they are walked recursively but not added to archive
		if d.IsDir() {
			// Check if we should skip this directory entirely
			if !shouldInclude(relative) {
				return filepath.SkipDir
			}
			return nil
		}

		// For files, check if they should be included
		if !shouldInclude(relative) {
			return nil
		}

		// Add the file to the archive
		return a.addToArchive(relative, d)
	}
}

// addToArchive adds a file to the archive
func (a *Archive) addToArchive(relative string, d ufs.DirEntry) error {
	// Get file info directly from the filesystem path
	absolutePath := filepath.Join(a.Filesystem.Path(), relative)
	s, err := os.Lstat(absolutePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return errors.WrapIff(err, "failed to get file info for '%s'", relative)
	}

	// Skip socket files as they are unsupported by archive/tar
	if s.Mode()&fs.ModeSocket != 0 {
		return nil
	}

	// Handle symlinks
	var target string
	if s.Mode()&fs.ModeSymlink != 0 {
		target, err = os.Readlink(absolutePath)
		if err != nil {
			if !os.IsNotExist(err) {
				log.WithField("path", relative).WithField("readlink_err", err.Error()).Warn("failed reading symlink for target path; skipping...")
			}
			return nil
		}
	}

	// Create tar header
	header, err := tar.FileInfoHeader(s, filepath.ToSlash(target))
	if err != nil {
		return errors.WrapIff(err, "failed to get tar#FileInfoHeader for '%s'", relative)
	}

	// Set the header name to the relative path
	if s.Mode()&fs.ModeSymlink == 0 {
		header.Name = relative
	}

	// Write the header
	if err := a.w.WriteHeader(header); err != nil {
		return errors.WrapIff(err, "failed to write tar#FileInfoHeader for '%s'", relative)
	}

	// Skip if no content to write
	if header.Size < 1 {
		return nil
	}

	// Prepare buffer
	var buf []byte
	if header.Size < memory {
		buf = make([]byte, header.Size)
	} else {
		buf = pool.Get().([]byte)
		defer func() {
			buf = make([]byte, memory)
			pool.Put(buf)
		}()
	}

	// Open and copy file content
	f, err := os.Open(filepath.Join(a.Filesystem.Path(), relative))
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return errors.WrapIff(err, "failed to open '%s' for copying", header.Name)
	}
	defer f.Close()

	if _, err := io.CopyBuffer(a.w, io.LimitReader(f, header.Size), buf); err != nil {
		return errors.WrapIff(err, "failed to copy '%s' to archive", header.Name)
	}

	return nil
}
