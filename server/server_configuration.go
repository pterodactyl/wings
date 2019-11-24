package server

import (
	"github.com/pkg/errors"
	"gopkg.in/yaml.v2"
	"os"
)

// Writes the server configuration to the disk. The saved configuration will be returned
// back to the calling function to use if desired.
func (s *Server) WriteConfigurationToDisk() ([]byte, error) {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	f, err := os.Create("data/servers/" + s.Uuid + ".yml")
	if err != nil {
		return nil, errors.WithStack(err)
	}
	defer f.Close()

	b, err := yaml.Marshal(&s)
	if err != nil {
		return nil, errors.WithStack(err)
	}

	if _, err := f.Write(b); err != nil {
		return nil, errors.WithStack(err)
	}

	return b, nil
}