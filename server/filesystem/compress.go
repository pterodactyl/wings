package filesystem

import (
	"context"
	"emperror.dev/errors"
	"fmt"
	"github.com/karrick/godirwalk"
	"github.com/pterodactyl/wings/server/backup"
	ignore "github.com/sabhiram/go-gitignore"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

// Given a directory, iterate through all of the files and folders within it and determine
// if they should be included in the output based on an array of ignored matches. This uses
// standard .gitignore formatting to make that determination.
//
// If no ignored files are passed through you'll get the entire directory listing.
func (fs *Filesystem) GetIncludedFiles(dir string, ignored []string) (*backup.IncludedFiles, error) {
	cleaned, err := fs.SafePath(dir)
	if err != nil {
		return nil, err
	}

	i, err := ignore.CompileIgnoreLines(ignored...)
	if err != nil {
		return nil, errors.WithStack(err)
	}

	// Walk through all of the files and directories on a server. This callback only returns
	// files found, and will keep walking deeper and deeper into directories.
	inc := new(backup.IncludedFiles)

	err = godirwalk.Walk(cleaned, &godirwalk.Options{
		Unsorted: true,
		Callback: func(p string, e *godirwalk.Dirent) error {
			sp := p
			if e.IsSymlink() {
				sp, err = fs.SafePath(p)
				if err != nil {
					if IsBadPathResolutionError(err) {
						return godirwalk.SkipThis
					}

					return err
				}
			}

			// Only push files into the result array since archives can't create an empty directory within them.
			if !e.IsDir() {
				// Avoid unnecessary parsing if there are no ignored files, nothing will match anyways
				// so no reason to call the function.
				if len(ignored) == 0 || !i.MatchesPath(strings.TrimPrefix(sp, fs.Path()+"/")) {
					inc.Push(sp)
				}
			}

			// We can't just abort if the path is technically ignored. It is possible there is a nested
			// file or folder that should not be excluded, so in this case we need to just keep going
			// until we get to a final state.
			return nil
		},
	})

	return inc, errors.WithStackIf(err)
}

// Compresses all of the files matching the given paths in the specified directory. This function
// also supports passing nested paths to only compress certain files and folders when working in
// a larger directory. This effectively creates a local backup, but rather than ignoring specific
// files and folders, it takes an allow-list of files and folders.
//
// All paths are relative to the dir that is passed in as the first argument, and the compressed
// file will be placed at that location named `archive-{date}.tar.gz`.
func (fs *Filesystem) CompressFiles(dir string, paths []string) (os.FileInfo, error) {
	cleanedRootDir, err := fs.SafePath(dir)
	if err != nil {
		return nil, err
	}

	// Take all of the paths passed in and merge them together with the root directory we've gotten.
	for i, p := range paths {
		paths[i] = filepath.Join(cleanedRootDir, p)
	}

	cleaned, err := fs.ParallelSafePath(paths)
	if err != nil {
		return nil, err
	}

	inc := new(backup.IncludedFiles)
	// Iterate over all of the cleaned paths and merge them into a large object of final file
	// paths to pass into the archiver. As directories are encountered this will drop into them
	// and look for all of the files.
	for _, p := range cleaned {
		f, err := os.Stat(p)
		if err != nil {
			fs.error(err).WithField("path", p).Debug("failed to stat file or directory for compression")
			continue
		}

		if !f.IsDir() {
			inc.Push(p)
		} else {
			err := godirwalk.Walk(p, &godirwalk.Options{
				Unsorted: true,
				Callback: func(p string, e *godirwalk.Dirent) error {
					sp := p
					if e.IsSymlink() {
						// Ensure that any symlinks are properly resolved to their final destination. If
						// that destination is outside the server directory skip over this entire item, otherwise
						// use the resolved location for the rest of this function.
						sp, err = fs.SafePath(p)
						if err != nil {
							if IsBadPathResolutionError(err) {
								return godirwalk.SkipThis
							}

							return err
						}
					}

					if !e.IsDir() {
						inc.Push(sp)
					}

					return nil
				},
			})

			if err != nil {
				return nil, err
			}
		}
	}

	a := &backup.Archive{TrimPrefix: fs.Path(), Files: inc}
	d := path.Join(cleanedRootDir, fmt.Sprintf("archive-%s.tar.gz", strings.ReplaceAll(time.Now().Format(time.RFC3339), ":", "")))

	if err := a.Create(d, context.Background()); err != nil {
		return nil, errors.WithStackIf(err)
	}

	f, err := os.Stat(d)
	if err != nil {
		_ = os.Remove(d)

		return nil, err
	}

	if err := fs.hasSpaceFor(f.Size()); err != nil {
		_ = os.Remove(d)

		return nil, err
	}

	fs.addDisk(f.Size())

	return f, nil
}
