package server

import (
	"github.com/pkg/errors"
	"github.com/pterodactyl/wings/server/filesystem"
	"os"
)

func (s *Server) Filesystem() *filesystem.Filesystem {
	return s.fs
}

// Ensures that the data directory for the server instance exists.
func (s *Server) EnsureDataDirectoryExists() error {
	if _, err := os.Stat(s.fs.Path()); err != nil && !os.IsNotExist(err) {
		return errors.WithStack(err)
	} else if err != nil {
		// Create the server data directory because it does not currently exist
		// on the system.
		if err := os.MkdirAll(s.fs.Path(), 0700); err != nil {
			return errors.WithStack(err)
		}

		if err := s.fs.Chown("/"); err != nil {
			s.Log().WithField("error", err).Warn("failed to chown server data directory")
		}
	}

	return nil
}
