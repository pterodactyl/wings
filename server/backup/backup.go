package backup

import (
	"archive/zip"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync/atomic"

	"emperror.dev/errors"
	"github.com/apex/log"
	"github.com/mholt/archives"
	"golang.org/x/sync/errgroup"

	"github.com/pterodactyl/wings/config"
	"github.com/pterodactyl/wings/internal/ufs"
	"github.com/pterodactyl/wings/remote"
	"github.com/pterodactyl/wings/server/filesystem"
)

// Keep existing format for transfers - DO NOT CHANGE THIS
var format = archives.CompressedArchive{
	Compression: archives.Gz{},
	Archival:    archives.Tar{},
	Extraction:  archives.Tar{},
}

type AdapterType string

const (
	LocalBackupAdapter AdapterType = "wings"
	S3BackupAdapter    AdapterType = "s3"
)

// RestoreCallback is a generic restoration callback that exists for both local
// and remote backups allowing the files to be restored.
type RestoreCallback func(file string, info fs.FileInfo, r io.ReadCloser) error

// noinspection GoNameStartsWithPackageName
type BackupInterface interface {
	// SetClient sets the API request client on the backup interface.
	SetClient(remote.Client)
	// Identifier returns the UUID of this backup as tracked by the panel
	// instance.
	Identifier() string
	// WithLogContext attaches additional context to the log output for this
	// backup.
	WithLogContext(map[string]interface{})
	// Generate creates a backup in whatever the configured source for the
	// specific implementation is.
	Generate(context.Context, *filesystem.Filesystem, string) (*ArchiveDetails, error)
	// Ignored returns the ignored files for this backup instance.
	Ignored() string
	// Checksum returns a SHA1 checksum for the generated backup.
	Checksum() ([]byte, error)
	// Size returns the size of the generated backup.
	Size() (int64, error)
	// Path returns the path to the backup on the machine. This is not always
	// the final storage location of the backup, simply the location we're using
	// to store it until it is moved to the final spot.
	Path() string
	// Details returns details about the archive.
	Details(context.Context, []remote.BackupPart) (*ArchiveDetails, error)
	// Remove removes a backup file.
	Remove() error
	// Restore is called when a backup is ready to be restored to the disk from
	// the given source. Not every backup implementation will support this nor
	// will every implementation require a reader be provided.
	Restore(context.Context, io.Reader, RestoreCallback) error
}

type Backup struct {
	// The UUID of this backup object. This must line up with a backup from
	// the panel instance.
	Uuid string `json:"uuid"`

	// An array of files to ignore when generating this backup. This should be
	// compatible with a standard .gitignore structure.
	Ignore string `json:"ignore"`

	client     remote.Client
	adapter    AdapterType
	logContext map[string]interface{}
}

func (b *Backup) SetClient(c remote.Client) {
	b.client = c
}

func (b *Backup) Identifier() string {
	return b.Uuid
}

// Path returns the path for this specific backup - now using .zip extension
func (b *Backup) Path() string {
	return path.Join(config.Get().System.BackupDirectory, b.Identifier()+".zip")
}

// Size returns the size of the generated backup.
func (b *Backup) Size() (int64, error) {
	st, err := os.Stat(b.Path())
	if err != nil {
		return 0, err
	}

	return st.Size(), nil
}

// Checksum returns the SHA256 checksum of a backup.
func (b *Backup) Checksum() ([]byte, error) {
	h := sha1.New()

	f, err := os.Open(b.Path())
	if err != nil {
		return nil, err
	}
	defer f.Close()

	buf := make([]byte, 1024*4)
	if _, err := io.CopyBuffer(h, f, buf); err != nil {
		return nil, err
	}

	return h.Sum(nil), nil
}

// Details returns both the checksum and size of the archive currently stored on
// the disk to the caller.
func (b *Backup) Details(ctx context.Context, parts []remote.BackupPart) (*ArchiveDetails, error) {
	ad := ArchiveDetails{ChecksumType: "sha1", Parts: parts}
	g, ctx := errgroup.WithContext(ctx)

	g.Go(func() error {
		resp, err := b.Checksum()
		if err != nil {
			return err
		}
		ad.Checksum = hex.EncodeToString(resp)
		return nil
	})

	g.Go(func() error {
		s, err := b.Size()
		if err != nil {
			return err
		}
		ad.Size = s
		return nil
	})

	if err := g.Wait(); err != nil {
		return nil, errors.WithStackDepth(err, 1)
	}
	return &ad, nil
}

func (b *Backup) Ignored() string {
	return b.Ignore
}

// WithLogContext attaches additional context to the log output for this backup.
func (b *Backup) WithLogContext(c map[string]interface{}) {
	b.logContext = c
}

// Remove removes a backup from the system.
func (b *Backup) Remove() error {
	return os.Remove(b.Path())
}

// Generate creates a ZIP backup of the server files.
func (b *Backup) Generate(ctx context.Context, fs *filesystem.Filesystem, ignore string) (*ArchiveDetails, error) {
	b.logContext = map[string]interface{}{
		"backup_id": b.Uuid,
		"adapter":   b.adapter,
	}

	logger := b.log().WithField("subsystem", "backup")
	logger.Info("starting backup generation")

	// Create backup archive (ZIP format)
	ba := &BackupArchive{
		BaseDirectory: "/",
		Ignore:        ignore,
		Filesystem:    fs,
	}

	// Create the backup file
	f, err := os.OpenFile(b.Path(), os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	// Stream the backup to the file
	if err := ba.Stream(ctx, f); err != nil {
		return nil, err
	}

	logger.Info("backup generation completed")
	return b.Details(ctx, nil)
}

// Restore is not implemented for local backups currently.
func (b *Backup) Restore(ctx context.Context, r io.Reader, callback RestoreCallback) error {
	return errors.New("local backup restoration is not implemented")
}

// Returns a logger instance for this backup with the additional context fields
// assigned to the output.
func (b *Backup) log() *log.Entry {
	l := log.WithField("backup", b.Identifier()).WithField("adapter", b.adapter)
	for k, v := range b.logContext {
		l = l.WithField(k, v)
	}
	return l
}

// BackupArchive represents a ZIP archive specifically for backups
type BackupArchive struct {
	BaseDirectory string
	Files         []string
	Ignore        string
	Progress      *int64
	Filesystem    *filesystem.Filesystem
}

// Stream creates a ZIP archive and streams it to the writer
func (ba *BackupArchive) Stream(ctx context.Context, w io.Writer) error {
	if ba.Filesystem == nil {
		return errors.New("filesystem: BackupArchive.Filesystem is unset")
	}

	ba.BaseDirectory = strings.TrimPrefix(ba.BaseDirectory, "/")

	if filesLen := len(ba.Files); filesLen > 0 {
		files := make([]string, filesLen)
		for i, f := range ba.Files {
			if !strings.HasPrefix(f, ba.Filesystem.Path()) {
				files[i] = f
				continue
			}
			files[i] = strings.TrimPrefix(strings.TrimPrefix(f, ba.Filesystem.Path()), "/")
		}
		ba.Files = files
	}

	// Choose compression method for ZIP
	var compressionMethod uint16
	switch config.Get().System.Backups.CompressionLevel {
	case "none":
		compressionMethod = zip.Store
	case "best_compression":
		compressionMethod = zip.Deflate
	default:
		compressionMethod = zip.Deflate
	}

	// Create ZIP writer
	zw := zip.NewWriter(w)
	defer zw.Close()

	// Handle file filtering logic
	var shouldInclude func(relative string) bool
	if len(ba.Files) == 0 && len(ba.Ignore) > 0 {
		// Simple ignore pattern matching - can be enhanced with proper gitignore parsing
		shouldInclude = func(relative string) bool {
			ignoreLines := strings.Split(ba.Ignore, "\n")
			for _, line := range ignoreLines {
				line = strings.TrimSpace(line)
				if line == "" || strings.HasPrefix(line, "#") {
					continue
				}
				// Simple pattern matching - you may want to use filepath.Match or a gitignore library
				if matched, _ := filepath.Match(line, relative); matched {
					return false
				}
				if strings.Contains(relative, line) {
					return false
				}
			}
			return true
		}
	} else if len(ba.Files) > 0 {
		shouldInclude = func(relative string) bool {
			for _, selectedPath := range ba.Files {
				// Exact match for files or folders
				if relative == selectedPath {
					return true
				}
				// Check if this file/folder is inside a selected folder
				if strings.HasPrefix(relative, selectedPath+"/") {
					return true
				}
			}
			return false
		}
	} else {
		shouldInclude = func(relative string) bool { return true }
	}

	// Walk the directory and add files to ZIP using recursive ReadDir
	return ba.walkDirectory(ctx, zw, ba.BaseDirectory, "", shouldInclude, compressionMethod)
}

// walkDirectory recursively walks directories and adds files to the ZIP
func (ba *BackupArchive) walkDirectory(ctx context.Context, zw *zip.Writer, currentDir, relativePath string, shouldInclude func(string) bool, compressionMethod uint16) error {
	entries, err := ba.Filesystem.ReadDir(currentDir)
	if err != nil {
		return err
	}

	for _, entry := range entries {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		entryName := entry.Name()
		var fullPath, relativeName string

		if currentDir == "" || currentDir == "." {
			fullPath = entryName
		} else {
			fullPath = filepath.Join(currentDir, entryName)
		}

		if relativePath == "" {
			relativeName = entryName
		} else {
			relativeName = filepath.Join(relativePath, entryName)
		}

		if !shouldInclude(relativeName) {
			if entry.IsDir() {
				continue // Skip entire directory
			}
			continue // Skip file
		}

		if err := ba.addEntryToZip(ctx, zw, fullPath, relativeName, entry, compressionMethod); err != nil {
			return err
		}

		// If it's a directory, recurse into it
		if entry.IsDir() {
			if err := ba.walkDirectory(ctx, zw, fullPath, relativeName, shouldInclude, compressionMethod); err != nil {
				return err
			}
		}
	}

	return nil
}

// addEntryToZip adds a single entry to the ZIP archive
func (ba *BackupArchive) addEntryToZip(ctx context.Context, zw *zip.Writer, fullPath, relativeName string, entry ufs.DirEntry, compressionMethod uint16) error {
	abs := filepath.Join(ba.Filesystem.Path(), fullPath)
	info, err := os.Lstat(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	// Skip creating a file entry for directories; just recurse
	if info.IsDir() {
		return nil
	}

	header, err := zip.FileInfoHeader(info)
	if err != nil {
		return err
	}
	header.Name = relativeName
	header.Method = compressionMethod

	// Handle symlinks by storing the target in the comment field
	if info.Mode()&fs.ModeSymlink != 0 {
		target, err := os.Readlink(abs)
		if err == nil {
			header.Comment = target
		}
	}

	fw, err := zw.CreateHeader(header)
	if err != nil {
		return err
	}

	// For symlinks, no content to write
	if info.Mode()&fs.ModeSymlink != 0 {
		return nil
	}

	// Copy file contents
	file, err := os.Open(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer file.Close()

	written, err := io.Copy(fw, file)
	if err != nil {
		return err
	}

	// Progress tracking if used
	if ba.Progress != nil {
		atomic.AddInt64(ba.Progress, written)
	}

	return nil
}

type ArchiveDetails struct {
	Checksum     string              `json:"checksum"`
	ChecksumType string              `json:"checksum_type"`
	Size         int64               `json:"size"`
	Parts        []remote.BackupPart `json:"parts"`
}

// ToRequest returns a request object.
func (ad *ArchiveDetails) ToRequest(successful bool) remote.BackupRequest {
	return remote.BackupRequest{
		Checksum:     ad.Checksum,
		ChecksumType: ad.ChecksumType,
		Size:         ad.Size,
		Successful:   successful,
		Parts:        ad.Parts,
	}
}
