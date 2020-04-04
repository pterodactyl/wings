package server

import (
	"crypto/sha256"
	"encoding/hex"
	"github.com/mholt/archiver/v3"
	"github.com/pterodactyl/wings/config"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
)

// Archiver represents a Server Archiver.
type Archiver struct {
	Server *Server
}

// ArchivePath returns the path to the server's archive.
func (a *Archiver) ArchivePath() string {
	return filepath.Join(config.Get().System.Data, ".archives", a.ArchiveName())
}

// ArchiveName returns the name of the server's archive.
func (a *Archiver) ArchiveName() string {
	return a.Server.Uuid + ".tar.gz"
}

// Exists returns a boolean based off if the archive exists.
func (a *Archiver) Exists() bool {
	if _, err := os.Stat(a.ArchivePath()); os.IsNotExist(err) {
		return false
	}

	return true
}

// Stat .
func (a *Archiver) Stat() (*Stat, error) {
	return a.Server.Filesystem.unsafeStat(a.ArchivePath())
}

// Archive creates an archive of the server.
func (a *Archiver) Archive() error {
	path := a.Server.Filesystem.Path()

	// Get the list of root files and directories to archive.
	var files []string
	fileInfo, err := ioutil.ReadDir(path)
	if err != nil {
		return err
	}

	for _, file := range fileInfo {
		files = append(files, filepath.Join(path, file.Name()))
	}

	return archiver.NewTarGz().Archive(files, a.ArchivePath())
}

// Checksum computes a SHA256 checksum of the server's archive.
func (a *Archiver) Checksum() (string, error) {
	file, err := os.Open(a.ArchivePath())
	if err != nil {
		return "", err
	}
	defer file.Close()

	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}

	return hex.EncodeToString(hash.Sum(nil)), nil
}
