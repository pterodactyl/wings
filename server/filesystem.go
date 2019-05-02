package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"github.com/gabriel-vasile/mimetype"
	"go.uber.org/zap"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Error returned when there is a bad path provided to one of the FS calls.
var InvalidPathResolution = errors.New("invalid path resolution")

type Filesystem struct {
	// The root directory where all of the server data is contained. By default
	// this is going to be /srv/daemon-data but can vary depending on the system.
	Root string

	// The server object associated with this Filesystem.
	Server *Server
}

// Returns the root path that contains all of a server's data.
func (fs *Filesystem) Path() string {
	return filepath.Join(fs.Root, fs.Server.Uuid)
}

// Normalizes a directory being passed in to ensure the user is not able to escape
// from their data directory. After normalization if the directory is still within their home
// path it is returned. If they managed to "escape" an error will be returned.
//
// This logic is actually copied over from the SFTP server code. Ideally that eventually
// either gets ported into this application, or is able to make use of this package.
func (fs *Filesystem) SafePath(p string) (string, error) {
	var nonExistentPathResolution string

	// Calling filpath.Clean on the joined directory will resolve it to the absolute path,
	// removing any ../ type of resolution arguments, and leaving us with a direct path link.
	r := filepath.Clean(filepath.Join(fs.Path(), p))

	// At the same time, evaluate the symlink status and determine where this file or folder
	// is truly pointing to.
	p, err := filepath.EvalSymlinks(r)
	if err != nil && !os.IsNotExist(err) {
		return "", err
	} else if os.IsNotExist(err) {
		// The requested directory doesn't exist, so at this point we need to iterate up the
		// path chain until we hit a directory that _does_ exist and can be validated.
		parts := strings.Split(filepath.Dir(r), "/")

		var try string
		// Range over all of the path parts and form directory pathings from the end
		// moving up until we have a valid resolution or we run out of paths to try.
		for k := range parts {
			try = strings.Join(parts[:(len(parts) - k)], "/")

			if !strings.HasPrefix(try, fs.Path()) {
				break
			}

			t, err := filepath.EvalSymlinks(try)
			if err == nil {
				nonExistentPathResolution = t
				break
			}
		}
	}

	// If the new path doesn't start with their root directory there is clearly an escape
	// attempt going on, and we should NOT resolve this path for them.
	if nonExistentPathResolution != "" {
		if !strings.HasPrefix(nonExistentPathResolution, fs.Path()) {
			return "", InvalidPathResolution
		}

		// If the nonExistentPathResoltion variable is not empty then the initial path requested
		// did not exist and we looped through the pathway until we found a match. At this point
		// we've confirmed the first matched pathway exists in the root server directory, so we
		// can go ahead and just return the path that was requested initially.
		return r, nil
	}

	// If the requested directory from EvalSymlinks begins with the server root directory go
	// ahead and return it. If not we'll return an error which will block any further action
	// on the file.
	if strings.HasPrefix(p, fs.Path()) {
		return p, nil
	}

	return "", InvalidPathResolution
}

// Determines if the directory a file is trying to be added to has enough space available
// for the file to be written to.
//
// Because determining the amount of space being used by a server is a taxing operation we
// will load it all up into a cache and pull from that as long as the key is not expired.
func (fs *Filesystem) HasSpaceAvailable() bool {
	var space = fs.Server.Build.DiskSpace

	// If space is -1 or 0 just return true, means they're allowed unlimited.
	if space <= 0 {
		return true
	}

	var size int64
	if x, exists := fs.Server.Cache.Get("disk_used"); exists {
		size = x.(int64)
	}

	// If there is no size its either because there is no data (in which case running this function
	// will have effectively no impact), or there is nothing in the cache, in which case we need to
	// grab the size of their data directory. This is a taxing operation, so we want to store it in
	// the cache once we've gotten it.
	if size == 0 {
		if size, err := fs.DirectorySize("/"); err != nil {
			zap.S().Warnw("failed to determine directory size", zap.String("server", fs.Server.Uuid), zap.Error(err))
		} else {
			fs.Server.Cache.Set("disk_used", size, time.Minute*5)
		}
	}

	// Determine if their folder size, in bytes, is smaller than the amount of space they've
	// been allocated.
	return (size / 1024.0 / 1024.0) <= space
}

// Determines the directory size of a given location by running parallel tasks to iterate
// through all of the folders. Returns the size in bytes. This can be a fairly taxing operation
// on locations with tons of files, so it is recommended that you cache the output.
func (fs *Filesystem) DirectorySize(dir string) (int64, error) {
	var size int64
	var wg sync.WaitGroup

	cleaned, err := fs.SafePath(dir)
	if err != nil {
		return 0, err
	}

	files, err := ioutil.ReadDir(cleaned)
	if err != nil {
		return 0, err
	}

	// Iterate over all of the files and directories. If it is a file, immediately add its size
	// to the total size being returned. If we're dealing with a directory, call this function
	// on a seperate thread until we have gotten the size of everything nested within the given
	// directory.
	for _, f := range files {
		if f.IsDir() {
			wg.Add(1)

			go func(p string) {
				defer wg.Done()

				s, _ := fs.DirectorySize(p)
				size += s
			}(filepath.Join(cleaned, f.Name()))
		} else {
			size += f.Size()
		}
	}

	wg.Wait()

	return size, nil
}

// Reads a file on the system and returns it as a byte representation in a file
// reader. This is not the most memory efficient usage since it will be reading the
// entirety of the file into memory.
func (fs *Filesystem) Readfile(p string) (io.Reader, error) {
	cleaned, err := fs.SafePath(p)
	if err != nil {
		return nil, err
	}

	b, err := ioutil.ReadFile(cleaned)
	if err != nil {
		return nil, err
	}

	return bytes.NewReader(b), nil
}

// Delete a file or folder from the system. If a folder location is passed in the
// folder and all of its contents are deleted.
func (fs *Filesystem) DeleteFile(p string) error {
	cleaned, err := fs.SafePath(p)
	if err != nil {
		return err
	}

	return os.RemoveAll(cleaned)
}

// Defines the stat struct object.
type Stat struct {
	Info     os.FileInfo
	Mimetype string
}

func (s *Stat) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Name      string `json:"name"`
		Created   string `json:"created"`
		Modified  string `json:"modified"`
		Mode      string `json:"mode"`
		Size      int64  `json:"size"`
		Directory bool   `json:"directory"`
		File      bool   `json:"file"`
		Symlink   bool   `json:"symlink"`
		Mime      string `json:"mime"`
	}{
		Name:      s.Info.Name(),
		Created:   s.CTime().String(),
		Modified:  s.Info.ModTime().String(),
		Mode:      s.Info.Mode().String(),
		Size:      s.Info.Size(),
		Directory: s.Info.IsDir(),
		File:      !s.Info.IsDir(),
		Symlink:   s.Info.Mode().Perm()&os.ModeSymlink != 0,
		Mime:      s.Mimetype,
	})
}

// Stats a file or folder and returns the base stat object from go along with the
// MIME data that can be used for editing files.
func (fs *Filesystem) Stat(p string) (*Stat, error) {
	cleaned, err := fs.SafePath(p)
	if err != nil {
		return nil, err
	}

	s, err := os.Stat(cleaned)
	if err != nil {
		return nil, err
	}

	var m = "inode/directory"
	if !s.IsDir() {
		m, _, err = mimetype.DetectFile(cleaned)
		if err != nil {
			return nil, err
		}
	}

	st := &Stat{
		Info:     s,
		Mimetype: m,
	}

	return st, nil
}

// Lists the contents of a given directory and returns stat information about each
// file and folder within it.
func (fs *Filesystem) ListDirectory(p string) ([]*Stat, error) {
	cleaned, err := fs.SafePath(p)
	if err != nil {
		return nil, err
	}

	files, err := ioutil.ReadDir(cleaned)
	if err != nil {
		return nil, err
	}

	var wg sync.WaitGroup

	// You must initialize the output of this directory as a non-nil value otherwise
	// when it is marshaled into a JSON object you'll just get 'null' back, which will
	// break the panel badly.
	out := make([]*Stat, 0)

	// Iterate over all of the files and directories returned and perform an async process
	// to get the mime-type for them all.
	for _, file := range files {
		wg.Add(1)

		go func(f os.FileInfo) {
			defer wg.Done()

			var m = "inode/directory"
			if !f.IsDir() {
				m, _, _ = mimetype.DetectFile(filepath.Join(cleaned, f.Name()))
			}

			out = append(out, &Stat{
				Info:     f,
				Mimetype: m,
			})
		}(file)
	}

	wg.Wait()

	// Sort the output alphabetically to begin with since we've run the output
	// through an asynchronous process and the order is gonna be very random.
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Info.Name() == out[j].Info.Name() || out[i].Info.Name() < out[j].Info.Name() {
			return true
		}

		return false
	})

	// Then, sort it so that directories are listed first in the output. Everything
	// will continue to be alphabetized at this point.
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].Info.IsDir()
	})

	return out, nil
}
