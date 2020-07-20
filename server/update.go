package server

import (
	"encoding/json"
	"github.com/buger/jsonparser"
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
func (s *Server) UpdateDataStructure(data []byte, background bool) error {
	src := new(Configuration)
	if err := json.Unmarshal(data, src); err != nil {
		return errors.WithStack(err)
	}

	// Don't allow obviously corrupted data to pass through into this function. If the UUID
	// doesn't match something has gone wrong and the API is attempting to meld this server
	// instance into a totally different one, which would be bad.
	if src.Uuid != "" && s.Uuid != "" && src.Uuid != s.Uuid {
		return errors.New("attempting to merge a data stack with an invalid UUID")
	}

	// Grab a copy of the configuration to work on.
	c := *s.Config()

	// Lock our copy of the configuration since the defered unlock will end up acting upon this
	// new memory address rather than the old one. If we don't lock this, the defered unlock will
	// cause a panic when it goes to run. However, since we only update s.cfg at the end, if there
	// is an error before that point we'll still properly unlock the original configuration for the
	// server.
	c.mu.Lock()

	// Lock the server configuration while we're doing this merge to avoid anything
	// trying to overwrite it or make modifications while we're sorting out what we
	// need to do.
	s.cfg.mu.Lock()
	defer s.cfg.mu.Unlock()

	// Merge the new data object that we have received with the existing server data object
	// and then save it to the disk so it is persistent.
	if err := mergo.Merge(&c, src, mergo.WithOverride); err != nil {
		return errors.WithStack(err)
	}

	// Don't explode if we're setting CPU limits to 0. Mergo sees that as an empty value
	// so it won't override the value we've passed through in the API call. However, we can
	// safely assume that we're passing through valid data structures here. I foresee this
	// backfiring at some point, but until then...
	//
	// We'll go ahead and do this with swap as well.
	c.Build.CpuLimit = src.Build.CpuLimit
	c.Build.Swap = src.Build.Swap
	c.Build.DiskSpace = src.Build.DiskSpace

	// Mergo can't quite handle this boolean value correctly, so for now we'll just
	// handle this edge case manually since none of the other data passed through in this
	// request is going to be boolean. Allegedly.
	if v, err := jsonparser.GetBoolean(data, "container", "oom_disabled"); err != nil {
		if err != jsonparser.KeyPathNotFoundError {
			return errors.WithStack(err)
		}
	} else {
		c.Container.OomDisabled = v
	}

	// Mergo also cannot handle this boolean value.
	if v, err := jsonparser.GetBoolean(data, "suspended"); err != nil {
		if err != jsonparser.KeyPathNotFoundError {
			return errors.WithStack(err)
		}
	} else {
		c.Suspended = v
	}

	// Environment and Mappings should be treated as a full update at all times, never a
	// true patch, otherwise we can't know what we're passing along.
	if src.EnvVars != nil && len(src.EnvVars) > 0 {
		c.EnvVars = src.EnvVars
	}

	if src.Allocations.Mappings != nil && len(src.Allocations.Mappings) > 0 {
		c.Allocations.Mappings = src.Allocations.Mappings
	}

	if src.Mounts != nil && len(src.Mounts) > 0 {
		c.Mounts = src.Mounts
	}

	// Update the configuration once we have a lock on the configuration object.
	s.cfg = c
	s.Uuid = c.Uuid

	if background {
		go s.runBackgroundActions()
	}

	return nil
}

// Runs through different actions once a server's configuration has been persisted
// to the disk. This function does not return anything as any failures should be logged
// but have no effect on actually updating the server itself.
//
// These tasks run in independent threads where relevant to speed up any updates
// that need to happen.
func (s *Server) runBackgroundActions() {
	// Check if the s is now suspended, and if so and the process is not terminated
	// yet, do it immediately.
	if s.IsSuspended() && s.GetState() != ProcessOfflineState {
		s.Log().Info("server suspended with running process state, terminating now")

		if err := s.Environment.WaitForStop(10, true); err != nil {
			s.Log().WithField("error", err).Warn("failed to terminate server environment after suspension")
		}
	}

	if !s.IsSuspended() {
		// Update the environment in place, allowing memory and CPU usage to be adjusted
		// on the fly without the user needing to reboot (theoretically).
		s.Log().Info("performing server limit modification on-the-fly")
		if err := s.Environment.InSituUpdate(); err != nil {
			s.Log().WithField("error", err).Warn("failed to perform on-the-fly update of the server environment")
		}
	}
}
