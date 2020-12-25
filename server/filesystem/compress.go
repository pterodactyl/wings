package filesystem

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

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

	a := &Archive{BasePath: cleanedRootDir, Files: cleaned}
	d := path.Join(
		cleanedRootDir,
		fmt.Sprintf("archive-%s.tar.gz", strings.ReplaceAll(time.Now().Format(time.RFC3339), ":", "")),
	)

	if err := a.Create(d); err != nil {
		return nil, err
	}

	f, err := os.Stat(d)
	if err != nil {
		_ = os.Remove(d)
		return nil, err
	}

	if err := fs.HasSpaceFor(f.Size()); err != nil {
		_ = os.Remove(d)
		return nil, err
	}

	fs.addDisk(f.Size())

	return f, nil
}
