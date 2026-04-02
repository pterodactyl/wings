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
	"github.com/juju/ratelimit"
	"github.com/klauspost/pgzip"
	ignore "github.com/sabhiram/go-gitignore"

	"github.com/pterodactyl/wings/config"
	"github.com/pterodactyl/wings/internal/progress"
)

const memory = 4 * 1024

var ErrNoSpaceAvailable = errors.Sentinel("archive: no space available on disk")

var pool = sync.Pool{
	New: func() interface{} {
		b := make([]byte, memory)
		return b
	},
}

// TarProgress .
type TarProgress struct {
	*tar.Writer
	p *progress.Progress
}

// NewTarProgress returns a new progress writer for the tar file. This is a wrapper
// around the standard writer with a progress instance embedded.
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

type ArchiveOption func(a *Archive) error

type Archive struct {
	root     *os.Root
	dir      string
	pw       *TarProgress
	ignored  *ignore.GitIgnore
	matching *ignore.GitIgnore
	p        *progress.Progress
}

// NewArchive returns a new archive instance that can be used for generating an
// archive of files and folders within the provided os.Root. The "dir" value is
// a child directory within the `os.Root` instance.
func NewArchive(r *os.Root, dir string, opts ...ArchiveOption) (*Archive, error) {
	a := &Archive{root: r, dir: dir}
	for _, opt := range opts {
		if err := opt(a); err != nil {
			return nil, errors.Wrap(err, "server/filesystem: archive: failed to apply callback option")
		}
	}
	return a, nil
}

func WithProgress(p *progress.Progress) ArchiveOption {
	return func(a *Archive) error {
		a.p = p
		return nil
	}
}

func WithIgnored(files []string) ArchiveOption {
	return func(a *Archive) error {
		if a.matching != nil {
			return errors.NewPlain("cannot create an archive with both ignored and matching configurations")
		}

		a.ignored = ignore.CompileIgnoreLines(files...)

		return nil
	}
}

func WithMatching(files []string) ArchiveOption {
	return func(a *Archive) error {
		if a.ignored != nil {
			return errors.NewPlain("cannot create an archive with both ignored and matching configurations")
		}

		lines := make([]string, len(files))
		for _, f := range files {
			// The old archiver logic just accepted an array of paths to include in the
			// archive and did rudimentary logic to determine if they should be included.
			// This newer logic makes use of the gitignore (flipped to make it an allowlist),
			// but to do that we need to make sure all the provided values here start with a
			// slash; otherwise files/folders nested deeply might be unintentionally included.
			lines = append(lines, "/"+strings.TrimPrefix(f, "/"))
		}

		a.matching = ignore.CompileIgnoreLines(lines...)

		return nil
	}
}

func (a *Archive) Progress() *progress.Progress {
	return a.p
}

// Create .
func (a *Archive) Create(ctx context.Context, f *os.File) error {
	// Select a writer based off of the WriteLimit configuration option. If there is no
	// write limit use the file as the writer.
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

// Stream walks the given root directory and generates an archive from the
// provided files.
func (a *Archive) Stream(ctx context.Context, w io.Writer) error {
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

	a.pw = NewTarProgress(tw, a.p)
	defer a.pw.Close()

	r, err := a.root.OpenRoot(normalize(a.dir))
	if err != nil {
		return errors.Wrap(err, "server/filesystem: archive: failed to acquire root dir instance")
	}
	defer r.Close()

	base := strings.TrimRight(r.Name(), "./")
	return filepath.WalkDir(base, a.walker(ctx, base))
}

// Callback function used to determine if a given file should be included in the archive
// being generated.
func (a *Archive) walker(ctx context.Context, base string) fs.WalkDirFunc {
	return func(path string, de fs.DirEntry, err error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		if err != nil {
			return fs.SkipDir
		}

		path = strings.TrimPrefix(path, base)
		if a.ignored != nil && a.ignored.MatchesPath(path) {
			return nil
		}

		if a.matching != nil && !a.matching.MatchesPath(path) {
			return nil
		}

		// Add the file to the archive, if it is nested in a directory,
		// the directory will be automatically "created" in the archive.
		return a.addToArchive(path)
	}
}

// Adds a given file path to the final archive being created.
func (a *Archive) addToArchive(p string) error {
	p = normalize(p)
	s, err := a.root.Lstat(p)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return errors.Wrap(err, "server/filesystem: archive: failed to stat file")
	}

	// Skip socket files as they are unsupported by archive/tar.
	// Error will come from tar#FileInfoHeader: "archive/tar: sockets not supported"
	if s.Mode()&fs.ModeSocket != 0 {
		return nil
	}

	// Resolve the symlink target if the file is a symlink.
	var target string
	if s.Mode()&fs.ModeSymlink != 0 {
		// This intentionally uses [os.Readlink] and not the [os.Root] instance. We need to
		// know the actual target for the symlink, even if outside the server directory, so
		// that we can restore it properly.
		//
		// This target is only used for the sake of keeping everything correct in the archive;
		// we never read the target file contents.
		target, err = os.Readlink(filepath.Join(a.root.Name(), p))
		if err != nil {
			target = ""
		}
	}

	// Get the tar FileInfoHeader to add the file to the archive.
	header, err := tar.FileInfoHeader(s, target)
	if err != nil {
		return errors.Wrap(err, "server/filesystem: archive: failed to get file info header")
	}

	header.Name = p
	if err := a.pw.WriteHeader(header); err != nil {
		return errors.Wrap(err, "server/filesystem: archive: failed to write tar header")
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

	f, err := a.root.Open(p)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return errors.Wrap(err, "server/filesystem: archive: failed to open file for copying")
	}
	defer f.Close()

	if _, err := io.CopyBuffer(a.pw, io.LimitReader(f, header.Size), buf); err != nil {
		return errors.Wrap(err, "server/filesystem: archive: failed to copy file to archive")
	}

	return nil
}
