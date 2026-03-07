package filesystem

import (
	"archive/zip"
	"context"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"

	"emperror.dev/errors"

	"github.com/pterodactyl/wings/config"
	"github.com/pterodactyl/wings/internal/ufs"
)

// BackupArchive represents a ZIP archive specifically for backups
type BackupArchive struct {
	BaseDirectory string
	Files         []string
	Ignore        string
	Progress      *int64
	Filesystem    *Filesystem
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
