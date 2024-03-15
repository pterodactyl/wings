// SPDX-License-Identifier: BSD-2-Clause

// Some code in this file was derived from https://github.com/karrick/godirwalk.

//go:build unix

package ufs

import (
	"bytes"
	iofs "io/fs"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"unsafe"

	"golang.org/x/sys/unix"
)

type WalkDiratFunc func(dirfd int, name, relative string, d DirEntry, err error) error

func (fs *UnixFS) WalkDirat(dirfd int, name string, fn WalkDiratFunc) error {
	if dirfd == 0 {
		// TODO: proper validation, ideally a dedicated function.
		dirfd = int(fs.dirfd.Load())
	}
	info, err := fs.Lstatat(dirfd, name)
	if err != nil {
		err = fn(dirfd, name, name, nil, err)
	} else {
		b := newScratchBuffer()
		err = fs.walkDir(b, dirfd, name, name, iofs.FileInfoToDirEntry(info), fn)
	}
	if err == SkipDir || err == SkipAll {
		return nil
	}
	return err
}

func (fs *UnixFS) walkDir(b []byte, parentfd int, name, relative string, d DirEntry, walkDirFn WalkDiratFunc) error {
	if err := walkDirFn(parentfd, name, relative, d, nil); err != nil || !d.IsDir() {
		if err == SkipDir && d.IsDir() {
			// Successfully skipped directory.
			err = nil
		}
		return err
	}

	dirfd, err := fs.openat(parentfd, name, O_DIRECTORY|O_RDONLY, 0)
	if err != nil {
		return err
	}
	defer unix.Close(dirfd)

	dirs, err := fs.readDir(dirfd, name, b)
	if err != nil {
		// Second call, to report ReadDir error.
		err = walkDirFn(dirfd, name, relative, d, err)
		if err != nil {
			if err == SkipDir && d.IsDir() {
				err = nil
			}
			return err
		}
	}

	for _, d1 := range dirs {
		// TODO: the path.Join on this line may actually be partially incorrect.
		// If we are not walking starting at the root, relative will contain the
		// name of the directory we are starting the walk from, which will be
		// relative to the root of the filesystem instead of from where the walk
		// was initiated from.
		//
		// ref; https://github.com/pterodactyl/panel/issues/5030
		if err := fs.walkDir(b, dirfd, d1.Name(), path.Join(relative, d1.Name()), d1, walkDirFn); err != nil {
			if err == SkipDir {
				break
			}
			return err
		}
	}
	return nil
}

// ReadDirMap .
// TODO: document
func ReadDirMap[T any](fs *UnixFS, path string, fn func(DirEntry) (T, error)) ([]T, error) {
	dirfd, name, closeFd, err := fs.safePath(path)
	defer closeFd()
	if err != nil {
		return nil, err
	}
	fd, err := fs.openat(dirfd, name, O_DIRECTORY|O_RDONLY, 0)
	if err != nil {
		return nil, err
	}
	defer unix.Close(fd)

	entries, err := fs.readDir(fd, ".", nil)
	if err != nil {
		return nil, err
	}

	out := make([]T, len(entries))
	for i, e := range entries {
		idx := i
		e := e
		v, err := fn(e)
		if err != nil {
			return nil, err
		}
		out[idx] = v
	}
	return out, nil
}

// nameOffset is a compile time constant
const nameOffset = int(unsafe.Offsetof(unix.Dirent{}.Name))

func nameFromDirent(de *unix.Dirent) (name []byte) {
	// Because this GOOS' syscall.Dirent does not provide a field that specifies
	// the name length, this function must first calculate the max possible name
	// length, and then search for the NULL byte.
	ml := int(de.Reclen) - nameOffset

	// Convert syscall.Dirent.Name, which is array of int8, to []byte, by
	// overwriting Cap, Len, and Data slice header fields to the max possible
	// name length computed above, and finding the terminating NULL byte.
	//
	// TODO: is there an alternative to the deprecated SliceHeader?
	// SliceHeader was mainly deprecated due to it being misused for avoiding
	// allocations when converting a byte slice to a string, ref;
	// https://go.dev/issue/53003
	sh := (*reflect.SliceHeader)(unsafe.Pointer(&name))
	sh.Cap = ml
	sh.Len = ml
	sh.Data = uintptr(unsafe.Pointer(&de.Name[0]))

	if index := bytes.IndexByte(name, 0); index >= 0 {
		// Found NULL byte; set slice's cap and len accordingly.
		sh.Cap = index
		sh.Len = index
		return
	}

	// NOTE: This branch is not expected, but included for defensive
	// programming, and provides a hard stop on the name based on the structure
	// field array size.
	sh.Cap = len(de.Name)
	sh.Len = sh.Cap
	return
}

// modeTypeFromDirent converts a syscall defined constant, which is in purview
// of OS, to a constant defined by Go, assumed by this project to be stable.
//
// When the syscall constant is not recognized, this function falls back to a
// Stat on the file system.
func (fs *UnixFS) modeTypeFromDirent(fd int, de *unix.Dirent, osDirname, osBasename string) (FileMode, error) {
	switch de.Type {
	case unix.DT_REG:
		return 0, nil
	case unix.DT_DIR:
		return ModeDir, nil
	case unix.DT_LNK:
		return ModeSymlink, nil
	case unix.DT_CHR:
		return ModeDevice | ModeCharDevice, nil
	case unix.DT_BLK:
		return ModeDevice, nil
	case unix.DT_FIFO:
		return ModeNamedPipe, nil
	case unix.DT_SOCK:
		return ModeSocket, nil
	default:
		// If syscall returned unknown type (e.g., DT_UNKNOWN, DT_WHT), then
		// resolve actual mode by reading file information.
		return fs.modeType(fd, filepath.Join(osDirname, osBasename))
	}
}

// modeType returns the mode type of the file system entry identified by
// osPathname by calling os.LStat function, to intentionally not follow symbolic
// links.
//
// Even though os.LStat provides all file mode bits, we want to ensure same
// values returned to caller regardless of whether we obtained file mode bits
// from syscall or stat call. Therefore, mask out the additional file mode bits
// that are provided by stat but not by the syscall, so users can rely on their
// values.
func (fs *UnixFS) modeType(dirfd int, name string) (os.FileMode, error) {
	fi, err := fs.Lstatat(dirfd, name)
	if err == nil {
		return fi.Mode() & ModeType, nil
	}
	return 0, err
}

var minimumScratchBufferSize = os.Getpagesize()

func newScratchBuffer() []byte {
	return make([]byte, minimumScratchBufferSize)
}

func (fs *UnixFS) readDir(fd int, name string, b []byte) ([]DirEntry, error) {
	scratchBuffer := b
	if scratchBuffer == nil || len(scratchBuffer) < minimumScratchBufferSize {
		scratchBuffer = newScratchBuffer()
	}

	var entries []DirEntry
	var workBuffer []byte

	var sde unix.Dirent
	for {
		if len(workBuffer) == 0 {
			n, err := unix.Getdents(fd, scratchBuffer)
			if err != nil {
				if err == unix.EINTR {
					continue
				}
				return nil, convertErrorType(err)
			}
			if n <= 0 {
				// end of directory: normal exit
				return entries, nil
			}
			workBuffer = scratchBuffer[:n] // trim work buffer to number of bytes read
		}

		// "Go is like C, except that you just put `unsafe` all over the place".
		copy((*[unsafe.Sizeof(unix.Dirent{})]byte)(unsafe.Pointer(&sde))[:], workBuffer)
		workBuffer = workBuffer[sde.Reclen:] // advance buffer for next iteration through loop

		if sde.Ino == 0 {
			continue // inode set to 0 indicates an entry that was marked as deleted
		}

		nameSlice := nameFromDirent(&sde)
		nameLength := len(nameSlice)

		if nameLength == 0 || (nameSlice[0] == '.' && (nameLength == 1 || (nameLength == 2 && nameSlice[1] == '.'))) {
			continue
		}

		childName := string(nameSlice)
		mt, err := fs.modeTypeFromDirent(fd, &sde, name, childName)
		if err != nil {
			return nil, convertErrorType(err)
		}
		entries = append(entries, &dirent{name: childName, path: name, modeType: mt, dirfd: fd, fs: fs})
	}
}

// dirent stores the name and file system mode type of discovered file system
// entries.
type dirent struct {
	name     string
	path     string
	modeType FileMode

	dirfd int
	fs    *UnixFS
}

func (de dirent) Name() string {
	return de.name
}

func (de dirent) IsDir() bool {
	return de.modeType&ModeDir != 0
}

func (de dirent) Type() FileMode {
	return de.modeType
}

func (de dirent) Info() (FileInfo, error) {
	if de.fs == nil {
		return nil, nil
	}
	return de.fs.Lstatat(de.dirfd, de.name)
}

func (de dirent) Open() (File, error) {
	if de.fs == nil {
		return nil, nil
	}
	return de.fs.OpenFileat(de.dirfd, de.name, O_RDONLY, 0)
}

// reset releases memory held by entry err and name, and resets mode type to 0.
func (de *dirent) reset() {
	de.name = ""
	de.path = ""
	de.modeType = 0
}
