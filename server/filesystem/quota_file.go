package filesystem

import (
	"io"
	"sync"

	"github.com/pterodactyl/wings/internal/ufs"
)

type quotaFile struct {
	ufs.File

	fs   *Filesystem
	mu   sync.Mutex
	size int64
}

func newQuotaFile(fs *Filesystem, file ufs.File, size int64) ufs.File {
	return &quotaFile{File: file, fs: fs, size: size}
}

func (f *quotaFile) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return f.File.Write(p)
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	off, err := f.File.Seek(0, io.SeekCurrent)
	if err != nil {
		return 0, err
	}

	return f.writeAtLocked(p, off, func() (int, error) {
		return f.File.Write(p)
	})
}

func (f *quotaFile) WriteAt(p []byte, off int64) (int, error) {
	if off < 0 || len(p) == 0 {
		return f.File.WriteAt(p, off)
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	return f.writeAtLocked(p, off, func() (int, error) {
		return f.File.WriteAt(p, off)
	})
}

func (f *quotaFile) writeAtLocked(p []byte, off int64, write func() (int, error)) (int, error) {
	previousSize := f.size
	if growth := off + int64(len(p)) - previousSize; growth > 0 {
		if err := f.fs.reserveDisk(growth); err != nil {
			return 0, err
		}
	}

	n, err := write()
	writtenEnd := off + int64(n)
	if writtenEnd > previousSize {
		f.size = writtenEnd
	}

	if reserved := off + int64(len(p)) - previousSize; reserved > 0 {
		actual := int64(0)
		if writtenEnd > previousSize {
			actual = writtenEnd - previousSize
		}
		if actual < reserved {
			f.fs.adjustDisk(actual - reserved)
		}
	}

	return n, err
}

func (f *quotaFile) ReadFrom(r io.Reader) (int64, error) {
	return io.Copy(quotaFileWriter{file: f}, r)
}

func (f *quotaFile) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()

	st, statErr := f.File.Stat()
	closeErr := f.File.Close()
	if statErr == nil {
		f.fs.adjustDisk(st.Size() - f.size)
	}
	if statErr != nil {
		return statErr
	}
	return closeErr
}

type quotaFileWriter struct {
	file *quotaFile
}

func (w quotaFileWriter) Write(p []byte) (int, error) {
	return w.file.Write(p)
}
