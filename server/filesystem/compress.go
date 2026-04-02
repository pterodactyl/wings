package filesystem

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"emperror.dev/errors"
	"github.com/mholt/archives"
	"github.com/pterodactyl/wings/internal"
)

type extractOptions struct {
	dir    string
	file   string
	format archives.Format
	r      io.Reader
}

// CompressFiles compresses all the files matching the given paths in the
// specified directory. This function also supports passing nested paths to only
// compress certain files and folders when working in a larger directory. This
// effectively creates a local backup, but rather than ignoring specific files
// and folders, it takes an allowlist of files and folders.
//
// All paths are relative to the dir that is passed in as the first argument,
// and the compressed file will be placed at that location named
// `archive-{date}.tar.gz`.
func (fs *Filesystem) CompressFiles(ctx context.Context, dir string, paths []string) (os.FileInfo, error) {
	a, err := NewArchive(fs.root, dir, WithMatching(paths))
	if err != nil {
		return nil, errors.WrapIf(err, "server/filesystem: compress: failed to create archive instance")
	}

	n := fmt.Sprintf("archive-%s.tar.gz", strings.ReplaceAll(time.Now().Format(time.RFC3339), ":", ""))
	f, err := fs.root.OpenFile(normalize(filepath.Join(dir, n)), os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return nil, errors.Wrap(err, "server/filesystem: compress: failed to open file for writing")
	}
	defer f.Close()

	cw := internal.NewCountedWriter(f)
	// todo: eventing on the counted writer so that we can slowly increase the disk
	//  used value on the server as the file gets written?
	if err := a.Stream(ctx, cw); err != nil {
		return nil, errors.Wrap(err, "server/filesystem: compress: failed to write to disk")
	}
	if err := fs.HasSpaceFor(cw.BytesWritten()); err != nil {
		_ = fs.root.Remove(normalize(filepath.Join(dir, n)))
		return nil, err
	}
	fs.addDisk(cw.BytesWritten())

	return f.Stat()
}

// DecompressFile will decompress a file in a given directory by using the
// archiver tool to infer the file type and go from there. This will walk over
// all the files within the given archive and ensure that there is not a
// zip-slip attack being attempted by validating that the final path is within
// the server data directory.
func (fs *Filesystem) DecompressFile(ctx context.Context, dir string, file string) error {
	f, err := fs.root.Open(normalize(filepath.Join(dir, file)))
	if err != nil {
		return errors.Wrap(err, "server/filesystem: decompress: failed to open file")
	}
	defer f.Close()

	format, input, err := archives.Identify(ctx, filepath.Base(file), f)
	if err != nil {
		if errors.Is(err, archives.NoMatch) {
			return newFilesystemError(ErrCodeUnknownArchive, err)
		}
		return errors.Wrap(err, "server/filesystem: decompress: failed to identify archive format")
	}

	return fs.extractStream(ctx, extractOptions{dir: dir, file: file, format: format, r: input})
}

func (fs *Filesystem) extractStream(ctx context.Context, opts extractOptions) error {
	// See if it's a compressed archive, such as TAR or a ZIP
	ex, ok := opts.format.(archives.Extractor)
	if !ok {
		// If not, check if it's a single-file compression, such as
		// .log.gz, .sql.gz, and so on
		de, ok := opts.format.(archives.Decompressor)
		if !ok {
			return nil
		}

		p := filepath.Join(opts.dir, strings.TrimSuffix(opts.file, opts.format.Extension()))
		if err := fs.IsIgnored(p); err != nil {
			return nil
		}

		reader, err := de.OpenReader(opts.r)
		if err != nil {
			return errors.Wrap(err, "server/filesystem: decompress: failed to open reader")
		}
		defer reader.Close()

		// Open the file for creation/writing
		f, err := fs.root.OpenFile(normalize(p), os.O_WRONLY|os.O_CREATE, 0o644)
		if err != nil {
			return errors.Wrap(err, "server/filesystem: decompress: failed to open file")
		}
		defer f.Close()

		// Read in 4 KB chunks
		buf := make([]byte, 4096)
		for {
			n, err := reader.Read(buf)
			if n > 0 {
				if err := fs.HasSpaceFor(int64(n)); err != nil {
					return err
				}
				if _, err := f.Write(buf[:n]); err != nil {
					return errors.Wrap(err, "server/filesystem: decompress: failed to write")
				}
				fs.addDisk(int64(n))
			}

			if err != nil {
				if err == io.EOF {
					break
				}
				return errors.Wrap(err, "server/filesystem: decompress: failed to read")
			}
		}

		return nil
	}

	// Decompress and extract archive
	return ex.Extract(ctx, opts.r, func(ctx context.Context, f archives.FileInfo) error {
		if f.IsDir() {
			return nil
		}
		p := filepath.Join(opts.dir, f.NameInArchive)
		if err := fs.IsIgnored(p); err != nil {
			return nil
		}
		r, err := f.Open()
		if err != nil {
			return err
		}
		defer r.Close()
		if f.Mode()&os.ModeSymlink != 0 {
			// Try to create the symlink if it is in the archive, but don't hold up the process
			// if the file cannot be created. In that case just skip over it entirely.
			if f.LinkTarget != "" {
				p2 := strings.TrimLeft(filepath.Clean(p), string(filepath.Separator))
				if p2 == "" {
					p2 = "."
				}
				// We don't use [fs.Symlink] here because that normalizes the source directory for
				// consistency with the codebase. In this case when decompressing we want to just
				// accept the source without any normalization.
				if err := fs.root.Symlink(f.LinkTarget, p2); err != nil {
					if errors.Is(err, os.ErrNotExist) || IsPathError(err) || IsLinkError(err) {
						return nil
					}
					return errors.Wrap(err, "server/filesystem: decompress: failed to create symlink")
				}
			}
			return nil
		}

		if err := fs.Write(p, r, f.Size(), f.Mode().Perm()); err != nil {
			return errors.Wrap(err, "server/filesystem: decompress: failed to write file")
		}

		// Update the file modification time to the one set in the archive.
		if err := fs.Chtimes(p, f.ModTime(), f.ModTime()); err != nil {
			return errors.Wrap(err, "server/filesystem: decompress: failed to update file modification time")
		}

		return nil
	})
}

// ExtractStreamUnsafe .
func (fs *Filesystem) ExtractStreamUnsafe(ctx context.Context, dir string, r io.Reader) error {
	format, input, err := archives.Identify(ctx, "archive.tar.gz", r)
	if err != nil {
		if errors.Is(err, archives.NoMatch) {
			return newFilesystemError(ErrCodeUnknownArchive, err)
		}
		return err
	}
	return fs.extractStream(ctx, extractOptions{
		dir:    dir,
		format: format,
		r:      input,
	})
}
