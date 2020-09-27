package server

import (
	"crypto/sha256"
	"encoding/hex"
	"github.com/mholt/archiver/v3"
	"github.com/pkg/errors"
	"github.com/pterodactyl/wings/config"
	"github.com/pterodactyl/wings/server/filesystem"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
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
		return nil, errors.WithStack(err)
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
		f, err := a.Server.Filesystem().SafeJoin(path, file)
		if err != nil {
			return err
		}

		files = append(files, f)
	}

	stat, err := a.Stat()
	if err != nil && !os.IsNotExist(err) {
		return err
	}

	// Check if the file exists.
	if stat != nil {
		if err := os.Remove(a.Path()); err != nil {
			return err
		}
	}

	return archiver.NewTarGz().Archive(files, a.Path())
}

// DeleteIfExists deletes the archive if it exists.
func (a *Archiver) DeleteIfExists() error {
	stat, err := a.Stat()
	if err != nil && !os.IsNotExist(err) {
		return err
	}

	// Check if the file exists.
	if stat != nil {
		if err := os.Remove(a.Path()); err != nil {
			return err
		}
	}

	return nil
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
