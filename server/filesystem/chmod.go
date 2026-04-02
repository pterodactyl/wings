package filesystem

import (
	"fmt"
	fs2 "io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"emperror.dev/errors"
	"github.com/pterodactyl/wings/config"
)

func (fs *Filesystem) Chmod(path string, mode os.FileMode) error {
	path = strings.TrimLeft(filepath.Clean(path), "/")
	if path == "" {
		path = "."
	}
	if err := fs.root.Chmod(path, mode); err != nil {
		return errors.Wrap(err, "server/filesystem: chmod: failed to chmod path")
	}

	return nil
}

// Chown recursively iterates over a file or directory and sets the permissions on all the
// underlying files. Iterate over all the files and directories. If it is a file go ahead
// and perform the chown operation. Otherwise dig deeper into the directory until we've run
// out of directories to dig into.
func (fs *Filesystem) Chown(p string) error {
	p = normalize(p)
	uid := config.Get().System.User.Uid
	gid := config.Get().System.User.Gid
	if err := fs.root.Chown(p, uid, gid); err != nil {
		return errors.WrapIf(err, "server/filesystem: chown: failed to chown path")
	}

	// If this is not a directory, we can now return from the function; there is nothing
	// left that we need to do.
	if st, err := fs.root.Stat(p); err != nil || !st.IsDir() {
		if err == nil || errors.Is(err, os.ErrNotExist) {
			return nil
		}

		return errors.WrapIf(err, "server/filesystem: chown: failed to stat path")
	}

	rt := fs.rootPath
	if p == "." {
		r, err := fs.root.OpenRoot(p)
		if err != nil {
			return errors.WithStack(err)
		}
		defer r.Close()
		rt = r.Name()
	}

	// If this was a directory, begin walking over its contents recursively and ensure that all
	// the subfiles and directories get their permissions updated as well.
	return filepath.WalkDir(rt, func(path string, _ fs2.DirEntry, err error) error {
		path = normalize(path)
		if path == "." {
			return nil
		}

		if err := fs.root.Chown(path, uid, gid); err != nil {
			return errors.Wrap(err, fmt.Sprintf("server/filesystem: chown: failed to chown file"))
		}

		return nil
	})
}

func (fs *Filesystem) Chtimes(path string, atime, mtime time.Time) error {
	path = strings.TrimLeft(filepath.Clean(path), "/")
	if err := fs.root.Chtimes(path, atime, mtime); err != nil {
		return errors.Wrap(err, "server/filesystem: chtimes: failed to chtimes path")
	}

	return nil
}
