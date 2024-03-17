package filesystem

import (
	"context"
	"fmt"
	"io"
	iofs "io/fs"
	"path"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"emperror.dev/errors"
	"github.com/klauspost/compress/zip"
	"github.com/mholt/archiver/v4"

	"github.com/pterodactyl/wings/internal/ufs"
	"github.com/pterodactyl/wings/server/filesystem/archiverext"
)

// CompressFiles compresses all the files matching the given paths in the
// specified directory. This function also supports passing nested paths to only
// compress certain files and folders when working in a larger directory. This
// effectively creates a local backup, but rather than ignoring specific files
// and folders, it takes an allow-list of files and folders.
//
// All paths are relative to the dir that is passed in as the first argument,
// and the compressed file will be placed at that location named
// `archive-{date}.tar.gz`.
func (fs *Filesystem) CompressFiles(dir string, paths []string) (ufs.FileInfo, error) {
	a := &Archive{Filesystem: fs, BaseDirectory: dir, Files: paths}
	d := path.Join(
		dir,
		fmt.Sprintf("archive-%s.tar.gz", strings.ReplaceAll(time.Now().Format(time.RFC3339), ":", "")),
	)
	f, err := fs.unixFS.OpenFile(d, ufs.O_WRONLY|ufs.O_CREATE, 0o644)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	cw := ufs.NewCountedWriter(f)
	if err := a.Stream(context.Background(), cw); err != nil {
		return nil, err
	}
	if !fs.unixFS.CanFit(cw.BytesWritten()) {
		_ = fs.unixFS.Remove(d)
		return nil, newFilesystemError(ErrCodeDiskSpace, nil)
	}
	fs.unixFS.Add(cw.BytesWritten())
	return f.Stat()
}

func (fs *Filesystem) archiverFileSystem(ctx context.Context, p string) (iofs.FS, error) {
	f, err := fs.unixFS.Open(p)
	if err != nil {
		return nil, err
	}
	// Do not use defer to close `f`, it will likely be used later.

	format, _, err := archiver.Identify(filepath.Base(p), f)
	if err != nil && !errors.Is(err, archiver.ErrNoMatch) {
		_ = f.Close()
		return nil, err
	}

	// Reset the file reader.
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		_ = f.Close()
		return nil, err
	}

	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, err
	}

	if format != nil {
		switch ff := format.(type) {
		case archiver.Zip:
			// zip.Reader is more performant than ArchiveFS, because zip.Reader caches content information
			// and zip.Reader can open several content files concurrently because of io.ReaderAt requirement
			// while ArchiveFS can't.
			// zip.Reader doesn't suffer from issue #330 and #310 according to local test (but they should be fixed anyway)
			return zip.NewReader(f, info.Size())
		case archiver.Archival:
			return archiver.ArchiveFS{Stream: io.NewSectionReader(f, 0, info.Size()), Format: ff, Context: ctx}, nil
		case archiver.Compression:
			return archiverext.FileFS{File: f, Compression: ff}, nil
		}
	}
	_ = f.Close()
	return nil, archiver.ErrNoMatch
}

// SpaceAvailableForDecompression looks through a given archive and determines
// if decompressing it would put the server over its allocated disk space limit.
func (fs *Filesystem) SpaceAvailableForDecompression(ctx context.Context, dir string, file string) error {
	// Don't waste time trying to determine this if we know the server will have the space for
	// it since there is no limit.
	if fs.MaxDisk() <= 0 {
		return nil
	}

	fsys, err := fs.archiverFileSystem(ctx, filepath.Join(dir, file))
	if err != nil {
		if errors.Is(err, archiver.ErrNoMatch) {
			return newFilesystemError(ErrCodeUnknownArchive, err)
		}
		return err
	}

	var size atomic.Int64
	return iofs.WalkDir(fsys, ".", func(path string, d iofs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		select {
		case <-ctx.Done():
			// Stop walking if the context is canceled.
			return ctx.Err()
		default:
			info, err := d.Info()
			if err != nil {
				return err
			}
			if !fs.unixFS.CanFit(size.Add(info.Size())) {
				return newFilesystemError(ErrCodeDiskSpace, nil)
			}
			return nil
		}
	})
}

// DecompressFile will decompress a file in a given directory by using the
// archiver tool to infer the file type and go from there. This will walk over
// all the files within the given archive and ensure that there is not a
// zip-slip attack being attempted by validating that the final path is within
// the server data directory.
func (fs *Filesystem) DecompressFile(ctx context.Context, dir string, file string) error {
	f, err := fs.unixFS.Open(filepath.Join(dir, file))
	if err != nil {
		return err
	}
	defer f.Close()

	// Identify the type of archive we are dealing with.
	format, input, err := archiver.Identify(filepath.Base(file), f)
	if err != nil {
		if errors.Is(err, archiver.ErrNoMatch) {
			return newFilesystemError(ErrCodeUnknownArchive, err)
		}
		return err
	}

	return fs.extractStream(ctx, extractStreamOptions{
		Directory: dir,
		Format:    format,
		Reader:    input,
	})
}

// ExtractStreamUnsafe .
func (fs *Filesystem) ExtractStreamUnsafe(ctx context.Context, dir string, r io.Reader) error {
	format, input, err := archiver.Identify("archive.tar.gz", r)
	if err != nil {
		if errors.Is(err, archiver.ErrNoMatch) {
			return newFilesystemError(ErrCodeUnknownArchive, err)
		}
		return err
	}
	return fs.extractStream(ctx, extractStreamOptions{
		Directory: dir,
		Format:    format,
		Reader:    input,
	})
}

type extractStreamOptions struct {
	// The directory to extract the archive to.
	Directory string
	// File name of the archive.
	FileName string
	// Format of the archive.
	Format archiver.Format
	// Reader for the archive.
	Reader io.Reader
}

func (fs *Filesystem) extractStream(ctx context.Context, opts extractStreamOptions) error {
	// Decompress and extract archive
	ex, ok := opts.Format.(archiver.Extractor)
	if !ok {
		return nil
	}
	return ex.Extract(ctx, opts.Reader, nil, func(ctx context.Context, f archiver.File) error {
		if f.IsDir() {
			return nil
		}
		p := filepath.Join(opts.Directory, f.NameInArchive)
		// If it is ignored, just don't do anything with the file and skip over it.
		if err := fs.IsIgnored(p); err != nil {
			return nil
		}
		r, err := f.Open()
		if err != nil {
			return err
		}
		defer r.Close()
		if err := fs.Write(p, r, f.Size(), f.Mode()); err != nil {
			return wrapError(err, opts.FileName)
		}
		// Update the file modification time to the one set in the archive.
		if err := fs.Chtimes(p, f.ModTime(), f.ModTime()); err != nil {
			return wrapError(err, opts.FileName)
		}
		return nil
	})
}
