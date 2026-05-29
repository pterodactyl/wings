package filesystem

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"emperror.dev/errors"
	"github.com/apex/log"
	"github.com/gabriel-vasile/mimetype"
	ignore "github.com/sabhiram/go-gitignore"

	"github.com/pterodactyl/wings/config"
	"github.com/pterodactyl/wings/internal/ufs"
)

type Filesystem struct {
	unixFS *ufs.Quota

	mu                sync.RWMutex
	lastLookupTime    *usageLookupTime
	lookupInProgress  atomic.Bool
	diskCheckInterval time.Duration
	denylist          *ignore.GitIgnore

	isTest bool
}

// New creates a new Filesystem instance for a given server.
func New(root string, size int64, denylist []string) (*Filesystem, error) {
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, err
	}
	unixFS, err := ufs.NewUnixFS(root, config.UseOpenat2())
	if err != nil {
		return nil, err
	}
	quota := ufs.NewQuota(unixFS, size)

	return &Filesystem{
		unixFS: quota,

		diskCheckInterval: time.Duration(config.Get().System.DiskCheckInterval),
		lastLookupTime:    &usageLookupTime{},
		denylist:          ignore.CompileIgnoreLines(denylist...),
	}, nil
}

// Path returns the root path for the Filesystem instance.
func (fs *Filesystem) Path() string {
	return fs.unixFS.BasePath()
}

// ReadDir reads directory entries.
func (fs *Filesystem) ReadDir(path string) ([]ufs.DirEntry, error) {
	return fs.unixFS.ReadDir(path)
}

// ReadDirStat is like ReadDir except that it returns FileInfo for each entry
// instead of just a DirEntry.
func (fs *Filesystem) ReadDirStat(path string) ([]ufs.FileInfo, error) {
	return ufs.ReadDirMap(fs.unixFS.UnixFS, path, func(e ufs.DirEntry) (ufs.FileInfo, error) {
		return e.Info()
	})
}

// File returns a reader for a file instance as well as the stat information.
func (fs *Filesystem) File(p string) (ufs.File, Stat, error) {
	f, err := fs.unixFS.Open(p)
	if err != nil {
		return nil, Stat{}, err
	}
	st, err := statFromFile(f)
	if err != nil {
		_ = f.Close()
		return nil, Stat{}, err
	}
	return f, st, nil
}

func (fs *Filesystem) UnixFS() *ufs.UnixFS {
	return fs.unixFS.UnixFS
}

// Touch acts by creating the given file and path on the disk if it is not present
// already. If  it is present, the file is opened using the defaults which will truncate
// the contents. The opened file is then returned to the caller.
func (fs *Filesystem) Touch(p string, flag int) (ufs.File, error) {
	return fs.unixFS.Touch(p, flag, 0o644)
}

// Writefile writes a file to the system. If the file does not already exist one
// will be created. This will also properly recalculate the disk space used by
// the server when writing new files or modifying existing ones.
//
// DEPRECATED: use `Write` instead.
func (fs *Filesystem) Writefile(p string, r io.Reader) error {
	var currentSize int64
	st, err := fs.unixFS.Stat(p)
	if err != nil && !errors.Is(err, ufs.ErrNotExist) {
		return errors.Wrap(err, "server/filesystem: writefile: failed to stat file")
	} else if err == nil {
		if st.IsDir() {
			// TODO: resolved
			return errors.WithStack(&Error{code: ErrCodeIsDirectory, resolved: ""})
		}
		currentSize = st.Size()
	}

	// Touch the file and return the handle to it at this point. This will
	// create or truncate the file, and create any necessary parent directories
	// if they are missing.
	file, err := fs.unixFS.Touch(p, ufs.O_RDWR|ufs.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("error touching file: %w", err)
	}
	defer file.Close()

	// Do not use CopyBuffer here, it is wasteful as the file implements
	// io.ReaderFrom, which causes it to not use the buffer anyways.
	n, err := io.Copy(file, r)

	// Adjust the disk usage to account for the old size and the new size of the file.
	fs.unixFS.Add(n - currentSize)

	if err := fs.chownFile(p); err != nil {
		return fmt.Errorf("error chowning file: %w", err)
	}
	// Return the error from io.Copy.
	return err
}

func (fs *Filesystem) Write(p string, r io.Reader, newSize int64, mode ufs.FileMode) error {
	var currentSize int64
	st, err := fs.unixFS.Stat(p)
	if err != nil && !errors.Is(err, ufs.ErrNotExist) {
		return errors.Wrap(err, "server/filesystem: writefile: failed to stat file")
	} else if err == nil {
		if st.IsDir() {
			// TODO: resolved
			return errors.WithStack(&Error{code: ErrCodeIsDirectory, resolved: ""})
		}
		currentSize = st.Size()
	}

	// Check that the new size we're writing to the disk can fit. If there is currently
	// a file we'll subtract that current file size from the size of the buffer to determine
	// the amount of new data we're writing (or amount we're removing if smaller).
	if err := fs.HasSpaceFor(newSize - currentSize); err != nil {
		return err
	}

	// Touch the file and return the handle to it at this point. This will
	// create or truncate the file, and create any necessary parent directories
	// if they are missing.
	file, err := fs.unixFS.Touch(p, ufs.O_RDWR|ufs.O_TRUNC, mode)
	if err != nil {
		return err
	}
	defer file.Close()

	if newSize == 0 {
		// Subtract the previous size of the file if the new size is 0.
		fs.unixFS.Add(-currentSize)
	} else {
		// Do not use CopyBuffer here, it is wasteful as the file implements
		// io.ReaderFrom, which causes it to not use the buffer anyways.
		var n int64
		n, err = io.Copy(file, io.LimitReader(r, newSize))

		// Adjust the disk usage to account for the old size and the new size of the file.
		fs.unixFS.Add(n - currentSize)
	}

	if err := fs.chownFile(p); err != nil {
		return err
	}
	// Return any remaining error.
	return err
}

// CreateDirectory creates a new directory (name) at a specified path (p) for
// the server.
func (fs *Filesystem) CreateDirectory(name string, p string) error {
	return fs.unixFS.MkdirAll(filepath.Join(p, name), 0o755)
}

func (fs *Filesystem) Rename(oldpath, newpath string) error {
	return fs.unixFS.Rename(oldpath, newpath)
}

func (fs *Filesystem) Symlink(oldpath, newpath string) error {
	return fs.unixFS.Symlink(oldpath, newpath)
}

func (fs *Filesystem) chownFile(name string) error {
	if fs.isTest {
		return nil
	}

	uid := config.Get().System.User.Uid
	gid := config.Get().System.User.Gid
	return fs.unixFS.Lchown(name, uid, gid)
}

// Chown recursively iterates over a file or directory and sets the permissions on all of the
// underlying files. Iterate over all of the files and directories. If it is a file just
// go ahead and perform the chown operation. Otherwise dig deeper into the directory until
// we've run out of directories to dig into.
func (fs *Filesystem) Chown(p string) error {
	if fs.isTest {
		return nil
	}

	uid := config.Get().System.User.Uid
	gid := config.Get().System.User.Gid

	dirfd, name, closeFd, err := fs.unixFS.SafePath(p)
	defer closeFd()
	if err != nil {
		return err
	}

	// Start by just chowning the initial path that we received.
	if err := fs.unixFS.Lchownat(dirfd, name, uid, gid); err != nil {
		return errors.Wrap(err, "server/filesystem: chown: failed to chown path")
	}

	// If this is not a directory we can now return from the function, there is nothing
	// left that we need to do.
	if st, err := fs.unixFS.Lstatat(dirfd, name); err != nil || !st.IsDir() {
		return nil
	}

	// This walker is probably some of the most efficient code in Wings. It has
	// an internally re-used buffer for listing directory entries and doesn't
	// need to check if every individual path it touches is safe as the code
	// doesn't traverse symlinks, is immune to symlink timing attacks, and
	// gives us a dirfd and file name to make a direct syscall with.
	if err := fs.unixFS.WalkDirat(dirfd, name, func(dirfd int, name, _ string, info ufs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := fs.unixFS.Lchownat(dirfd, name, uid, gid); err != nil {
			return err
		}
		return nil
	}); err != nil {
		return fmt.Errorf("server/filesystem: chown: failed to chown during walk function: %w", err)
	}
	return nil
}

func (fs *Filesystem) Chmod(path string, mode ufs.FileMode) error {
	return fs.unixFS.Chmod(path, mode)
}

// Begin looping up to 50 times to try and create a unique copy file name. This will take
// an input of "file.txt" and generate "file copy.txt". If that name is already taken, it will
// then try to write "file copy 2.txt" and so on, until reaching 50 loops. At that point we
// won't waste anymore time, just use the current timestamp and make that copy.
//
// Could probably make this more efficient by checking if there are any files matching the copy
// pattern, and trying to find the highest number and then incrementing it by one rather than
// looping endlessly.
func (fs *Filesystem) findCopySuffix(dirfd int, name, extension string) (string, error) {
	var i int
	suffix := " copy"

	for i = 0; i < 51; i++ {
		if i > 0 {
			suffix = " copy " + strconv.Itoa(i)
		}

		n := name + suffix + extension
		// If we stat the file and it does not exist that means we're good to create the copy. If it
		// does exist, we'll just continue to the next loop and try again.
		if _, err := fs.unixFS.Lstatat(dirfd, n); err != nil {
			if !errors.Is(err, ufs.ErrNotExist) {
				return "", err
			}
			break
		}

		if i == 50 {
			suffix = "copy." + time.Now().Format(time.RFC3339)
		}
	}

	return name + suffix + extension, nil
}

// Copy copies a given file to the same location and appends a suffix to the
// file to indicate that it has been copied.
func (fs *Filesystem) Copy(p string) error {
	dirfd, name, closeFd, err := fs.unixFS.SafePath(p)
	defer closeFd()
	if err != nil {
		return err
	}
	source, err := fs.unixFS.OpenFileat(dirfd, name, ufs.O_RDONLY, 0)
	if err != nil {
		return err
	}
	defer source.Close()
	info, err := source.Stat()
	if err != nil {
		return err
	}
	if info.IsDir() || !info.Mode().IsRegular() {
		// If this is a directory or not a regular file, just throw a not-exist error
		// since anything calling this function should understand what that means.
		return ufs.ErrNotExist
	}
	currentSize := info.Size()

	// Check that copying this file wouldn't put the server over its limit.
	if err := fs.HasSpaceFor(currentSize); err != nil {
		return err
	}

	base := info.Name()
	extension := filepath.Ext(base)
	baseName := strings.TrimSuffix(base, extension)

	// Ensure that ".tar" is also counted as apart of the file extension.
	// There might be a better way to handle this for other double file extensions,
	// but this is a good workaround for now.
	if strings.HasSuffix(baseName, ".tar") {
		extension = ".tar" + extension
		baseName = strings.TrimSuffix(baseName, ".tar")
	}

	newName, err := fs.findCopySuffix(dirfd, baseName, extension)
	if err != nil {
		return err
	}
	dst, err := fs.unixFS.OpenFileat(dirfd, newName, ufs.O_WRONLY|ufs.O_CREATE, info.Mode())
	if err != nil {
		return err
	}

	// Do not use CopyBuffer here, it is wasteful as the file implements
	// io.ReaderFrom, which causes it to not use the buffer anyways.
	n, err := io.Copy(dst, io.LimitReader(source, currentSize))
	fs.unixFS.Add(n)

	if !fs.isTest {
		if err := fs.unixFS.Lchownat(dirfd, newName, config.Get().System.User.Uid, config.Get().System.User.Gid); err != nil {
			return err
		}
	}
	// Return the error from io.Copy.
	return err
}

// TruncateRootDirectory removes _all_ files and directories from a server's
// data directory and resets the used disk space to zero.
func (fs *Filesystem) TruncateRootDirectory() error {
	if err := os.RemoveAll(fs.Path()); err != nil {
		return err
	}
	if err := os.Mkdir(fs.Path(), 0o755); err != nil {
		return err
	}
	_ = fs.unixFS.Close()
	unixFS, err := ufs.NewUnixFS(fs.Path(), config.UseOpenat2())
	if err != nil {
		return err
	}
	var limit int64
	if fs.isTest {
		limit = 0
	} else {
		limit = fs.unixFS.Limit()
	}
	fs.unixFS = ufs.NewQuota(unixFS, limit)
	return nil
}

// Delete removes a file or folder from the system. Prevents the user from
// accidentally (or maliciously) removing their root server data directory.
func (fs *Filesystem) Delete(p string) error {
	return fs.unixFS.RemoveAll(p)
}

//type fileOpener struct {
//	fs   *Filesystem
//	busy uint
//}
//
//// Attempts to open a given file up to "attempts" number of times, using a backoff. If the file
//// cannot be opened because of a "text file busy" error, we will attempt until the number of attempts
//// has been exhaused, at which point we will abort with an error.
//func (fo *fileOpener) open(path string, flags int, perm ufs.FileMode) (ufs.File, error) {
//	for {
//		f, err := fo.fs.unixFS.OpenFile(path, flags, perm)
//
//		// If there is an error because the text file is busy, go ahead and sleep for a few
//		// hundred milliseconds and then try again up to three times before just returning the
//		// error back to the caller.
//		//
//		// Based on code from: https://github.com/golang/go/issues/22220#issuecomment-336458122
//		if err != nil && fo.busy < 3 && strings.Contains(err.Error(), "text file busy") {
//			time.Sleep(100 * time.Millisecond << fo.busy)
//			fo.busy++
//			continue
//		}
//
//		return f, err
//	}
//}

// ListDirectory lists the contents of a given directory and returns stat
// information about each file and folder within it.
func (fs *Filesystem) ListDirectory(p string) ([]Stat, error) {
	// Read entries from the path on the filesystem, using the mapped reader, so
	// we can map the DirEntry slice into a Stat slice with mimetype information.
	out, err := ufs.ReadDirMap(fs.unixFS.UnixFS, p, func(e ufs.DirEntry) (Stat, error) {
		info, err := e.Info()
		if err != nil {
			return Stat{}, err
		}

		var d string
		if e.Type().IsDir() {
			d = "inode/directory"
		} else {
			d = "application/octet-stream"
		}
		var m *mimetype.MIME
		if e.Type().IsRegular() {
			// TODO: I should probably find a better way to do this.
			eO := e.(interface {
				Open() (ufs.File, error)
			})
			f, err := eO.Open()
			if err != nil {
				return Stat{}, err
			}
			m, err = mimetype.DetectReader(f)
			if err != nil {
				log.Error(err.Error())
			}
			_ = f.Close()
		}

		st := Stat{FileInfo: info, Mimetype: d}
		if m != nil {
			st.Mimetype = m.String()
		}
		return st, nil
	})
	if err != nil {
		return nil, err
	}

	// Sort entries alphabetically.
	slices.SortStableFunc(out, func(a, b Stat) int {
		switch {
		case a.Name() == b.Name():
			return 0
		case a.Name() > b.Name():
			return 1
		default:
			return -1
		}
	})

	// Sort folders before other file types.
	slices.SortStableFunc(out, func(a, b Stat) int {
		switch {
		case a.IsDir() && b.IsDir():
			return 0
		case a.IsDir():
			return -1
		default:
			return 1
		}
	})

	return out, nil
}

func (fs *Filesystem) Chtimes(path string, atime, mtime time.Time) error {
	if fs.isTest {
		return nil
	}
	return fs.unixFS.Chtimes(path, atime, mtime)
}
