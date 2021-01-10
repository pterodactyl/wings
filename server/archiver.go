package server

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"

	"emperror.dev/errors"
	"github.com/mholt/archiver/v3"
	"github.com/pterodactyl/wings/config"
	"github.com/pterodactyl/wings/server/filesystem"
)

// Archiver represents a Server Archiver.
type Archiver struct {
	Server *Server
}

// Path returns the path to the server's archive.
func (a *Archiver) Path() string {
	return filepath.Join(config.Get().System.ArchiveDirectory, a.Name())
}

// Name returns the name of the server's archive.
func (a *Archiver) Name() string {
	return a.Server.Id() + ".tar.gz"
}

// Exists returns a boolean based off if the archive exists.
func (a *Archiver) Exists() bool {
	if _, err := os.Stat(a.Path()); os.IsNotExist(err) {
		return false
	}

	return true
}

// Stat stats the archive file.
func (a *Archiver) Stat() (*filesystem.Stat, error) {
	s, err := os.Stat(a.Path())
	if err != nil {
		return nil, err
	}

	return &filesystem.Stat{
		Info:     s,
		Mimetype: "application/tar+gzip",
	}, nil
}

// Archive creates an archive of the server and deletes the previous one.
func (a *Archiver) Archive() error {
	path := a.Server.Filesystem().Path()

	// Get the list of root files and directories to archive.
	var files []string
	fileInfo, err := ioutil.ReadDir(path)
	if err != nil {
		return err
	}

	for _, file := range fileInfo {
		f := filepath.Join(path, file.Name())
		// If the file is a symlink we cannot safely assume that the result of a filepath.Join() will be
		// a safe destination. We need to check if the file is a symlink, and if so pass off to the SafePath
		// function to resolve it to the final destination.
		//
		// ioutil.ReadDir() calls Lstat, so this will work correctly. If it did not call Lstat, but rather
		// just did a normal Stat call, this would fail since that would be looking at the symlink destination
		// and not the actual file in this listing.
		if file.Mode()&os.ModeSymlink != 0 {
			f, err = a.Server.Filesystem().SafePath(filepath.Join(path, file.Name()))
			if err != nil {
				return err
			}
		}

		files = append(files, f)
	}

	if err := a.DeleteIfExists(); err != nil {
		return err
	}

	return archiver.NewTarGz().Archive(files, a.Path())
}

// DeleteIfExists deletes the archive if it exists.
func (a *Archiver) DeleteIfExists() error {
	if _, err := a.Stat(); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}

		return err
	}

	return errors.WithMessage(os.Remove(a.Path()), "archiver: failed to delete archive from system")
}

// Checksum computes a SHA256 checksum of the server's archive.
func (a *Archiver) Checksum() (string, error) {
	file, err := os.Open(a.Path())
	if err != nil {
		return "", err
	}
	defer file.Close()

	hash := sha256.New()

	buf := make([]byte, 1024*4)
	if _, err := io.CopyBuffer(hash, file, buf); err != nil {
		return "", err
	}

	return hex.EncodeToString(hash.Sum(nil)), nil
}
