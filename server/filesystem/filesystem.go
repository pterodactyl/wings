package filesystem

import (
	"bufio"
	"io"
	fs2 "io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"emperror.dev/errors"
	"github.com/apex/log"
	"github.com/gabriel-vasile/mimetype"
	ignore "github.com/sabhiram/go-gitignore"

	"github.com/pterodactyl/wings/config"
	"github.com/pterodactyl/wings/system"
)

type Filesystem struct {
	mu                sync.RWMutex
	lastLookupTime    *usageLookupTime
	lookupInProgress  *system.AtomicBool
	diskUsed          int64
	diskCheckInterval time.Duration
	denylist          *ignore.GitIgnore

	// The maximum amount of disk space (in bytes) that this Filesystem instance can use.
	diskLimit int64

	// The root data directory path for this Filesystem instance.
	root     *os.Root
	rootPath string

	isTest bool
}

// New creates a new Filesystem instance for a given server.
func New(path string, size int64, denylist []string) (*Filesystem, error) {
	r, err := os.OpenRoot(path)
	if err != nil {
		return nil, errors.Wrap(err, "server/filesystem: failed to open root")
	}

	fs := &Filesystem{
		root:              r,
		rootPath:          path,
		diskLimit:         size,
		diskCheckInterval: time.Duration(config.Get().System.DiskCheckInterval),
		lastLookupTime:    &usageLookupTime{},
		lookupInProgress:  system.NewAtomicBool(false),
		denylist:          ignore.CompileIgnoreLines(denylist...),
	}

	return fs, nil
}

// normalize takes the input path, runs it through filepath.Clean and trims any
// leading forward slashes (since the os.Root method calls will fail otherwise).
// If the resulting path is an empty string, "." is returned which os.Root will
// understand as the base directory.
func normalize(path string) string {
	c := strings.TrimLeft(filepath.Clean(path), string(filepath.Separator))
	if c == "" {
		return "."
	}
	return c
}

// Path returns the root path for the Filesystem instance.
func (fs *Filesystem) Path() string {
	return fs.rootPath
}

// Close closes the underlying os.Root instance for the server.
func (fs *Filesystem) Close() error {
	if err := fs.root.Close(); err != nil {
		return errors.Wrap(err, "server/filesystem: failed to close root")
	}
	return nil
}

// File returns a reader for a file instance as well as the stat information.
func (fs *Filesystem) File(p string) (*os.File, Stat, error) {
	p = normalize(p)
	st, err := fs.Stat(p)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, Stat{}, newFilesystemError(ErrNotExist, err)
		}
		return nil, Stat{}, errors.WithStackIf(err)
	}
	if st.IsDir() {
		return nil, Stat{}, newFilesystemError(ErrCodeIsDirectory, nil)
	}
	f, err := fs.root.Open(p)
	if err != nil {
		return nil, Stat{}, errors.WithStackIf(err)
	}
	return f, st, nil
}

// Touch acts by creating the given file and path on the disk if it is not present
// already. If it is present, the file is opened using the defaults which will truncate
// the contents. The opened file is then returned to the caller.
func (fs *Filesystem) Touch(p string, flag int, mode os.FileMode) (*os.File, error) {
	p = normalize(p)
	o := &fileOpener{root: fs.root}
	f, err := o.open(p, flag, mode)
	if err == nil {
		return f, nil
	}
	if f != nil {
		_ = f.Close()
	}
	// If the error is not because it doesn't exist then we just need to bail at this point.
	if !errors.Is(err, os.ErrNotExist) {
		return nil, errors.Wrap(err, "server/filesystem: touch: failed to open file handle")
	}
	// Only create and chown the directory if it doesn't exist.
	if _, err := fs.root.Stat(filepath.Dir(p)); errors.Is(err, os.ErrNotExist) {
		// Create the path leading up to the file we're trying to create, setting the final perms
		// on it as we go.
		if err := fs.root.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return nil, errors.WrapIf(err, "server/filesystem: touch: failed to create directory tree")
		}
		if err := fs.Chown(filepath.Dir(p)); err != nil {
			return nil, errors.WrapIf(err, "server/filesystem: touch: failed to chown directory tree")
		}
	}
	// Try to open the file now that we have created the pathing necessary for it, and then
	// Chown that file so that the permissions don't mess with things.
	f, err = o.open(p, flag, mode)
	if err != nil {
		return nil, errors.Wrap(err, "server/filesystem: touch: failed to open file handle")
	}
	_ = fs.Chown(p)
	return f, nil
}

// Writefile writes a file to the system. If the file does not already exist one
// will be created. This will also properly recalculate the disk space used by
// the server when writing new files or modifying existing ones.
//
// deprecated 1.12.1 prefer the use of Filesystem.Write()
func (fs *Filesystem) Writefile(p string, r io.Reader) error {
	p = normalize(p)
	var currentSize int64
	// If the file does not exist on the system already go ahead and create the pathway
	// to it and an empty file. We'll then write to it later on after this completes.
	stat, err := fs.root.Stat(p)
	if err != nil && !os.IsNotExist(err) {
		return errors.Wrap(err, "server/filesystem: writefile: failed to stat file")
	} else if err == nil {
		if stat.IsDir() {
			return errors.WithStack(&Error{code: ErrCodeIsDirectory, resolved: stat.Name()})
		}
		currentSize = stat.Size()
	}

	br := bufio.NewReader(r)
	// Check that the new size we're writing to the disk can fit. If there is currently
	// a file we'll subtract that current file size from the size of the buffer to determine
	// the amount of new data we're writing (or amount we're removing if smaller).
	if err := fs.HasSpaceFor(int64(br.Size()) - currentSize); err != nil {
		return err
	}

	// Touch the file and return the handle to it at this point. This will create the file,
	// any necessary directories, and set the proper owner of the file.
	file, err := fs.Touch(p, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	defer file.Close()

	buf := make([]byte, 1024*4)
	sz, err := io.CopyBuffer(file, r, buf)

	// Adjust the disk usage to account for the old size and the new size of the file.
	fs.addDisk(sz - currentSize)

	return fs.Chown(p)
}

func (fs *Filesystem) Mkdir(p string, mode os.FileMode) error {
	if err := fs.root.Mkdir(normalize(p), mode); err != nil {
		return errors.Wrap(err, "server/filesystem: mkdir: failed to make directory")
	}
	return nil
}

// Write writes a file to the disk.
func (fs *Filesystem) Write(p string, r io.Reader, newSize int64, mode os.FileMode) error {
	st, err := fs.root.Stat(normalize(p))
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return errors.Wrap(err, "server/filesystem: write: failed to stat file")
		}
	}

	var c int64
	if err == nil {
		if st.IsDir() {
			return errors.WithStack(&Error{code: ErrCodeIsDirectory, resolved: normalize(p)})
		}
		c = st.Size()
	}

	if err := fs.HasSpaceFor(newSize - c); err != nil {
		return err
	}

	f, err := fs.Touch(p, os.O_RDWR|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return errors.Wrap(err, "server/filesystem: write: failed to touch file")
	}
	defer f.Close()

	if newSize == 0 {
		fs.addDisk(-c)
	} else {
		// Do not use CopyBuffer here; it is wasteful as the file implements
		// io.ReaderFrom, which causes it to not use the buffer anyway.
		n, err := io.Copy(f, io.LimitReader(r, newSize))
		// Always adjust the disk to account for cases where a partial copy occurs
		// and there is some new content on the disk.
		fs.addDisk(n - c)
		if err != nil {
			return errors.Wrap(err, "server/filesystem: write: failed to write file")
		}
	}

	// todo: might be unnecessary due to the `fs.Touch` call already doing this?
	return fs.Chown(p)
}

// CreateDirectory creates a new directory ("name") at a specified path ("p") for the server.
func (fs *Filesystem) CreateDirectory(name string, p string) error {
	return fs.root.MkdirAll(path.Join(normalize(p), name), 0o755)
}

// Rename moves (or renames) a file or directory.
func (fs *Filesystem) Rename(from string, to string) error {
	to = normalize(to)
	from = normalize(from)

	if from == "." || to == "." {
		return os.ErrExist
	}

	// If the target file or directory already exists the rename function will
	// fail, so just bail out now.
	if _, err := fs.root.Stat(to); err == nil {
		return os.ErrExist
	}

	d := strings.TrimLeft(filepath.Dir(to), "/")
	// Ensure that the directory we're moving into exists correctly on the system. Only do this if
	// we're not at the root directory level.
	if d != "" {
		if err := fs.root.MkdirAll(d, 0o755); err != nil {
			return errors.Wrap(err, "server/filesystem: failed to create directory tree")
		}
	}

	return fs.root.Rename(from, to)
}

// Begin looping up to 50 times to try and create a unique copy file name. This will take
// an input of "file.txt" and generate "file copy.txt". If that name is already taken, it will
// then try to write "file copy 2.txt" and so on, until reaching 50 loops. At that point we
// won't waste anymore time, just use the current timestamp and make that copy.
//
// Could probably make this more efficient by checking if there are any files matching the copy
// pattern, and trying to find the highest number and then incrementing it by one rather than
// looping endlessly.
func (fs *Filesystem) findCopySuffix(dir string, name string, extension string) (string, error) {
	var i int
	suffix := " copy"

	for i = 0; i < 51; i++ {
		if i > 0 {
			suffix = " copy " + strconv.Itoa(i)
		}

		n := name + suffix + extension
		// If we stat the file and it does not exist that means we're good to create the copy. If it
		// does exist, we'll just continue to the next loop and try again.
		if _, err := fs.Stat(path.Join(dir, n)); err != nil {
			if !errors.Is(err, os.ErrNotExist) {
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

// Copies a given file to the same location and appends a suffix to the file to indicate that
// it has been copied.
func (fs *Filesystem) Copy(p string) error {
	p = normalize(p)
	s, err := fs.root.Stat(p)
	if err != nil {
		return err
	} else if s.IsDir() || !s.Mode().IsRegular() {
		// If this is a directory or not a regular file, just throw a not-exist error
		// since anything calling this function should understand what that means.
		return os.ErrNotExist
	}

	// Check that copying this file wouldn't put the server over its limit.
	if err := fs.HasSpaceFor(s.Size()); err != nil {
		return err
	}

	base := filepath.Base(p)
	relative := strings.TrimSuffix(strings.TrimPrefix(p, fs.Path()), base)
	extension := filepath.Ext(base)
	name := strings.TrimSuffix(base, extension)

	// Ensure that ".tar" is also counted as apart of the file extension.
	// There might be a better way to handle this for other double file extensions,
	// but this is a good workaround for now.
	if strings.HasSuffix(name, ".tar") {
		extension = ".tar" + extension
		name = strings.TrimSuffix(name, ".tar")
	}

	source, err := fs.root.Open(p)
	if err != nil {
		return err
	}
	defer source.Close()

	n, err := fs.findCopySuffix(relative, name, extension)
	if err != nil {
		return err
	}

	return fs.Writefile(path.Join(relative, n), source)
}

// Symlink creates a symbolic link between the source and target paths. [os.Root].Symlink
// allows for the creation of a symlink that targets a file outside the root directory.
// This isn't the end of the world because the read is blocked through this system, and
// within a container it would just point to something in the readonly filesystem.
//
// There are also valid use-cases where a symlink might need to point to a file outside
// the server data directory for a server to operate correctly. Since everything in the
// filesystem runs through os.Root though we're protected from accidentally reading a
// sensitive file on the _host_ OS.
func (fs *Filesystem) Symlink(source, target string) error {
	source = normalize(source)
	target = normalize(target)

	// os.Root#Symlink allows for the creation of a symlink that targets a file outside
	// the root directory. This isn't the end of the world because the read is blocked
	// through this system, and within a container it would just point to something in the
	// readonly filesystem.
	//
	// However, just to avoid this propagating everywhere, *attempt* to block anything that
	// would be pointing to a location outside the root directory.
	if _, err := fs.root.Stat(source); err != nil {
		return errors.Wrap(err, "server/filesystem: symlink: failed to stat source")
	}

	// Yes -- this gap between the stat and symlink allows a TOCTOU vulnerability to exist,
	// but again we're layering this with the remaining logic that prevents this filesystem
	// from reading any symlinks or acting on any file that points outside the root as defined
	// by os.Root. The check above is mostly to prevent stupid mistakes or basic attempts to
	// get around this. If someone *really* wants to make these symlinks, they can. They can
	// also just create them from the running server process, and we still need to rely on our
	// own internal FS logic to detect and block those reads, which it does. Therefore, I am
	// not deeply concerned with this.
	if err := fs.root.Symlink(source, target); err != nil {
		return errors.Wrap(err, "server/filesystem: symlink: failed to create symlink")
	}

	return nil
}

// ReadDir returns all the contents of the given directory.
func (fs *Filesystem) ReadDir(p string) ([]fs2.DirEntry, error) {
	d, ok := fs.root.FS().(fs2.ReadDirFS)
	if !ok {
		return []fs2.DirEntry{}, errors.New("server/filesystem: readdir: could not init root fs")
	}

	e, err := d.ReadDir(normalize(p))
	if err != nil {
		return []fs2.DirEntry{}, errors.Wrap(err, "server/filesystem: readdir: failed to read directory")
	}

	return e, nil
}

// TruncateRootDirectory removes _all_ files and directories from a server's
// data directory and resets the used disk space to zero.
func (fs *Filesystem) TruncateRootDirectory() error {
	err := filepath.WalkDir(fs.rootPath, func(path string, d fs2.DirEntry, err error) error {
		p := normalize(strings.TrimPrefix(path, fs.rootPath))
		if p == "." {
			return nil
		}

		if err := fs.root.RemoveAll(p); err != nil {
			return err
		}

		if d.IsDir() {
			return filepath.SkipDir
		}

		return nil
	})

	if err != nil {
		go func() {
			// If there was an error, re-calculate the disk usage right away to account
			// for any partially removed files.
			_, _ = fs.updateCachedDiskUsage()
		}()

		return errors.Wrap(err, "server/filesystem: truncate: failed to walk root directory")
	}

	// Set the disk space back to zero.
	fs.addDisk(fs.diskUsed * -1)

	return nil
}

// Delete removes a file or folder from the system. Prevents the user from
// accidentally (or maliciously) removing their root server data directory.
func (fs *Filesystem) Delete(p string) error {
	p = normalize(p)
	if p == "." {
		return errors.New("server/filesystem: delete: cannot delete root directory")
	}

	st, err := fs.root.Lstat(p)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return errors.Wrap(err, "server/filesystem: delete: failed to stat file")
	}

	if st.IsDir() {
		if s, err := fs.DirectorySize(p); err == nil {
			fs.addDisk(-s)
		}
	} else {
		fs.addDisk(-st.Size())
	}

	return fs.root.RemoveAll(p)
}

type fileOpener struct {
	busy uint
	root *os.Root
}

// Attempts to open a given file up to "attempts" number of times, using a backoff. If the file
// cannot be opened because of a "text file busy" error, we will attempt until the number of attempts
// has been exhaused, at which point we will abort with an error.
func (fo *fileOpener) open(path string, flags int, mode os.FileMode) (*os.File, error) {
	for {
		f, err := fo.root.OpenFile(path, flags, mode)

		// If there is an error because the text file is busy, go ahead and sleep for a few
		// hundred milliseconds and then try again up to three times before just returning the
		// error back to the caller.
		//
		// Based on code from: https://github.com/golang/go/issues/22220#issuecomment-336458122
		if err != nil && fo.busy < 3 && strings.Contains(err.Error(), "text file busy") {
			time.Sleep(100 * time.Millisecond << fo.busy)
			fo.busy++
			continue
		}

		return f, err
	}
}

// ListDirectory lists the contents of a given directory and returns stat information
// about each file and folder within it. If you only need to know the contents of the
// directory and do not need mimetype information, call [Filesystem.ReadDir] directly
// instead.
func (fs *Filesystem) ListDirectory(p string) ([]Stat, error) {
	files, err := fs.ReadDir(p)
	if err != nil {
		return []Stat{}, err
	}

	var wg sync.WaitGroup

	// You must initialize the output of this directory as a non-nil value otherwise
	// when it is marshaled into a JSON object you'll just get 'null' back, which will
	// break the panel badly.
	out := make([]Stat, len(files))

	// Iterate over all the files and directories returned and perform an async process
	// to get the mime-type for them all.
	for i, file := range files {
		wg.Add(1)

		go func(idx int, d fs2.DirEntry) {
			defer wg.Done()

			fi, err := d.Info()
			if err != nil {
				log.WithField("error", err).WithField("path", filepath.Join(p, d.Name())).Warn("failed to retrieve directory entry info")
				return
			}

			if fi.IsDir() {
				out[idx] = Stat{FileInfo: fi, Mimetype: "inode/directory"}
				return
			}

			st := Stat{FileInfo: fi, Mimetype: "application/octet-stream"}

			// Don't try to detect the type on a pipe — this will just hang the application,
			// and you'll never get a response back.
			//
			// @see https://github.com/pterodactyl/panel/issues/4059
			if fi.Mode()&os.ModeNamedPipe == 0 {
				if f, err := fs.root.Open(normalize(filepath.Join(p, d.Name()))); err != nil {
					if !IsPathError(err) && !IsLinkError(err) {
						log.WithField("error", err).WithField("path", filepath.Join(p, d.Name())).Warn("error opening file for mimetype detection")
					}
				} else {
					if m, err := mimetype.DetectReader(f); err == nil {
						st.Mimetype = m.String()
					} else {
						log.WithField("error", err).WithField("path", filepath.Join(p, d.Name())).Warn("failed to detect mimetype for file")
					}
					_ = f.Close()
				}
			}

			out[idx] = st
		}(i, file)
	}

	wg.Wait()

	// Sort the output alphabetically to begin with since we've run the output
	// through an asynchronous process and the order is gonna be very random.
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Name() == out[j].Name() || out[i].Name() > out[j].Name() {
			return true
		}
		return false
	})

	// Then, sort it so that directories are listed first in the output. Everything
	// will continue to be alphabetized at this point.
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].IsDir()
	})

	return out, nil
}
