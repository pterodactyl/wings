package filesystem

import (
	"archive/tar"
	"context"
	"fmt"
	"io"
	iofs "io/fs"
	"math"
	"path"
	"path/filepath"
	"strings"
	"time"

	"emperror.dev/errors"
	"github.com/klauspost/compress/zip"
	"github.com/mholt/archives"

	"github.com/pterodactyl/wings/internal/ufs"
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
	name := fmt.Sprintf("archive-%s", strings.ReplaceAll(time.Now().Format(time.RFC3339), ":", ""))

	// Never write into an existing file, such as an archive created by another
	// request within the same second.
	var (
		d   string
		f   ufs.File
		err error
	)
	for i := 0; ; i++ {
		d = path.Join(dir, name+".tar.gz")
		if i > 0 {
			d = path.Join(dir, fmt.Sprintf("%s-%d.tar.gz", name, i))
		}
		f, err = fs.unixFS.OpenFile(d, ufs.O_WRONLY|ufs.O_CREATE|ufs.O_EXCL, 0o644)
		if err == nil {
			break
		}
		if !errors.Is(err, ufs.ErrExist) || i >= 100 {
			return nil, err
		}
	}

	// Count every write to the archive against the disk limit as it happens, so
	// that archives being created at the same time cannot each use all the space
	// that is left.
	qf := newQuotaFile(fs, f, 0)
	if err := a.Stream(context.Background(), qf); err != nil {
		_ = qf.Close()
		_ = fs.unixFS.Remove(d)
		return nil, err
	}
	st, err := qf.Stat()
	if err != nil {
		_ = qf.Close()
		return nil, err
	}
	if err := qf.Close(); err != nil {
		return nil, err
	}
	return st, nil
}

// maxArchiveEntryNameLength is the longest name an entry in an archive can have,
// which matches the longest path that can be created on Linux.
const maxArchiveEntryNameLength = 4096

// SpaceAvailableForDecompression looks through a given archive and determines
// if decompressing it would put the server over its allocated disk space limit.
//
// The entries are read one at a time rather than opening the archive as a file
// system, which is much faster for large archives.
func (fs *Filesystem) SpaceAvailableForDecompression(ctx context.Context, dir string, file string) error {
	// Don't waste time trying to determine this if we know the server will have the space for
	// it since there is no limit.
	if fs.MaxDisk() <= 0 {
		return nil
	}

	f, err := fs.unixFS.Open(filepath.Join(dir, file))
	if err != nil {
		return err
	}
	defer f.Close()

	format, _, err := archives.Identify(ctx, filepath.Base(file), f)
	if err != nil && !errors.Is(err, archives.NoMatch) {
		return err
	}
	// Reset the file reader.
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return err
	}
	info, err := f.Stat()
	if err != nil {
		return err
	}

	var size int64
	add := func(name string, n int64) error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		if len(name) > maxArchiveEntryNameLength {
			return errors.New("filesystem: archive contains an entry with a name that is too long")
		}
		if n <= 0 {
			return nil
		}
		if n > math.MaxInt64-size {
			return newFilesystemError(ErrCodeDiskSpace, nil)
		}
		size += n
		if !fs.unixFS.CanFit(size) {
			return newFilesystemError(ErrCodeDiskSpace, nil)
		}
		return nil
	}

	switch ff := format.(type) {
	case archives.Zip:
		// The sizes can be read straight from the central directory of a zip.
		reader, err := zip.NewReader(f, info.Size())
		if err != nil {
			return err
		}
		for _, e := range reader.File {
			if e.UncompressedSize64 > math.MaxInt64 {
				return newFilesystemError(ErrCodeDiskSpace, nil)
			}
			if err := add(e.Name, int64(e.UncompressedSize64)); err != nil {
				return err
			}
		}
		return nil
	case archives.Extraction:
		return ff.Extract(ctx, io.NewSectionReader(f, 0, info.Size()), func(ctx context.Context, e archives.FileInfo) error {
			return add(e.NameInArchive, e.Size())
		})
	case archives.Compression:
		// A single compressed file is checked against the disk limit as it is
		// decompressed, since its size is not known until then.
		return add(file, info.Size())
	}
	return newFilesystemError(ErrCodeUnknownArchive, archives.NoMatch)
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
	format, input, err := archives.Identify(ctx, filepath.Base(file), f)
	if err != nil {
		if errors.Is(err, archives.NoMatch) {
			return newFilesystemError(ErrCodeUnknownArchive, err)
		}
		return err
	}

	return fs.extractStream(ctx, extractStreamOptions{
		FileName:  file,
		Directory: dir,
		Format:    format,
		Reader:    input,
	})
}

// ExtractTransfer extracts the archive of a server that is being transferred to
// this node into the server's directory. Every file is kept, including ones the
// denylist would refuse, since they are the server's own files.
func (fs *Filesystem) ExtractTransfer(ctx context.Context, r io.Reader) error {
	format, input, err := archives.Identify(ctx, "archive.tar.gz", r)
	if err != nil {
		if errors.Is(err, archives.NoMatch) {
			return newFilesystemError(ErrCodeUnknownArchive, err)
		}
		return err
	}
	return fs.extractStream(ctx, extractStreamOptions{
		Directory:    "/",
		Format:       format,
		Reader:       input,
		KeepDenylist: true,
	})
}

type extractStreamOptions struct {
	// The directory to extract the archive to.
	Directory string
	// File name of the archive.
	FileName string
	// Format of the archive.
	Format archives.Format
	// Reader for the archive.
	Reader io.Reader
	// KeepDenylist extracts files the denylist would otherwise skip.
	KeepDenylist bool
}

func (fs *Filesystem) extractStream(ctx context.Context, opts extractStreamOptions) error {
	// See if it's a compressed archive, such as TAR or a ZIP
	ex, ok := opts.Format.(archives.Extractor)
	if !ok {
		// If not, check if it's a single-file compression, such as
		// .log.gz, .sql.gz, and so on
		de, ok := opts.Format.(archives.Decompressor)
		if !ok {
			return nil
		}

		// Strip the compression suffix
		p := filepath.Join(opts.Directory, strings.TrimSuffix(opts.FileName, opts.Format.Extension()))

		// Make sure it's not ignored
		if err := fs.IsIgnored(p); err != nil {
			return nil
		}

		reader, err := de.OpenReader(opts.Reader)
		if err != nil {
			return err
		}
		defer reader.Close()

		// Write the file, counting each write against the disk limit. The file is
		// removed if it does not fit.
		if _, err := fs.WriteFrom(p, reader); err != nil {
			return wrapError(err, opts.FileName)
		}
		return nil
	}

	// Decompress and extract archive
	return ex.Extract(ctx, opts.Reader, func(ctx context.Context, f archives.FileInfo) error {
		p := filepath.Join(opts.Directory, f.NameInArchive)
		// If it is ignored, just don't do anything with the entry and skip over it.
		if !opts.KeepDenylist {
			if err := fs.IsIgnored(p); err != nil {
				return nil
			}
		}
		// Create directories explicitly; an empty one has no file to create it
		// implicitly and would otherwise be dropped during extraction.
		if f.IsDir() {
			if err := fs.mkdirAll(p, 0o755); err != nil {
				return wrapError(err, opts.FileName)
			}
			return nil
		}
		if f.Mode()&iofs.ModeSymlink != 0 {
			if f.LinkTarget == "" {
				return nil
			}
			if err := fs.mkdirAll(filepath.Dir(p), 0o755); err != nil {
				return wrapError(err, opts.FileName)
			}
			if err := fs.Symlink(f.LinkTarget, p); err != nil && !errors.Is(err, ufs.ErrExist) {
				return wrapError(err, opts.FileName)
			}
			return nil
		}
		// Hard links, devices and other special files are not extracted.
		if !f.Mode().IsRegular() {
			return nil
		}
		if h, ok := f.Header.(*tar.Header); ok && h.Typeflag == tar.TypeLink {
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
