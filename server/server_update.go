package server

import (
	"encoding/json"
	"github.com/imdario/mergo"
	"github.com/pkg/errors"
)

// Merges data passed through in JSON form into the existing server object.
// Any changes to the build settings will apply immediately in the environment
// if the environment supports it.
//
// The server will be marked as requiring a rebuild on the next boot sequence,
// it is up to the specific environment to determine what needs to happen when
// that is the case.
func (s *Server) UpdateDataStructure(data []byte) error {
	src := Server{}
	if err := json.Unmarshal(data, &src); err != nil {
		return errors.WithStack(err)
	}

	// Merge the new data object that we have received with the existing server data object
	// and then save it to the disk so it is persistent.
	if err := mergo.Merge(&s, src); err != nil {
		return errors.WithStack(err)
	}

	s.Container.RebuildRequired = true
	if _, err := s.WriteConfigurationToDisk(); err != nil {
		return errors.WithStack(err)
	}

	return s.Environment.InSituUpdate()
}