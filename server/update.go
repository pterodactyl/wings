package server

import (
	"encoding/json"
	"github.com/buger/jsonparser"
	"github.com/imdario/mergo"
	"github.com/pkg/errors"
	"go.uber.org/zap"
)

// Merges data passed through in JSON form into the existing server object.
// Any changes to the build settings will apply immediately in the environment
// if the environment supports it.
//
// The server will be marked as requiring a rebuild on the next boot sequence,
// it is up to the specific environment to determine what needs to happen when
// that is the case.
func (s *Server) UpdateDataStructure(data []byte, background bool) error {
	src := new(Server)
	if err := json.Unmarshal(data, src); err != nil {
		return errors.WithStack(err)
	}

	// Don't allow obviously corrupted data to pass through into this function. If the UUID
	// doesn't match something has gone wrong and the API is attempting to meld this server
	// instance into a totally different one, which would be bad.
	if src.Uuid != "" && s.Uuid != "" && src.Uuid != s.Uuid {
		return errors.New("attempting to merge a data stack with an invalid UUID")
	}

	// Merge the new data object that we have received with the existing server data object
	// and then save it to the disk so it is persistent.
	if err := mergo.Merge(s, src, mergo.WithOverride); err != nil {
		return errors.WithStack(err)
	}

	// Don't explode if we're setting CPU limits to 0. Mergo sees that as an empty value
	// so it won't override the value we've passed through in the API call. However, we can
	// safely assume that we're passing through valid data structures here. I foresee this
	// backfiring at some point, but until then...
	//
	// We'll go ahead and do this with swap as well.
	s.Build.CpuLimit = src.Build.CpuLimit
	s.Build.Swap = src.Build.Swap
	s.Build.DiskSpace = src.Build.DiskSpace

	// Mergo can't quite handle this boolean value correctly, so for now we'll just
	// handle this edge case manually since none of the other data passed through in this
	// request is going to be boolean. Allegedly.
	if v, err := jsonparser.GetBoolean(data, "container", "oom_disabled"); err != nil {
		if err != jsonparser.KeyPathNotFoundError {
			return errors.WithStack(err)
		}
	} else {
		s.Container.OomDisabled = v
	}

	// Mergo also cannot handle this boolean value.
	if v, err := jsonparser.GetBoolean(data, "suspended"); err != nil {
		if err != jsonparser.KeyPathNotFoundError {
			return errors.WithStack(err)
		}
	} else {
		s.Suspended = v
	}

	// Environment and Mappings should be treated as a full update at all times, never a
	// true patch, otherwise we can't know what we're passing along.
	if src.EnvVars != nil && len(src.EnvVars) > 0 {
		s.EnvVars = src.EnvVars
	}

	if src.Allocations.Mappings != nil && len(src.Allocations.Mappings) > 0 {
		s.Allocations.Mappings = src.Allocations.Mappings
	}

	if background {
		s.runBackgroundActions()
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
	// Update the environment in place, allowing memory and CPU usage to be adjusted
	// on the fly without the user needing to reboot (theoretically).
	go func(server *Server) {
		server.Log().Info("performing server limit modification on-the-fly")
		if err := server.Environment.InSituUpdate(); err != nil {
			server.Log().WithField("error", err).Warn("failed to perform on-the-fly update of the server environment")
		}
	}(s)

	// Check if the server is now suspended, and if so and the process is not terminated
	// yet, do it immediately.
	go func(server *Server) {
		if server.Suspended && server.GetState() != ProcessOfflineState {
			zap.S().Infow("server suspended with running process state, terminating now", zap.String("server", server.Uuid))

			if err := server.Environment.WaitForStop(10, true); err != nil {
				zap.S().Warnw(
					"failed to stop server environment after seeing suspension",
					zap.String("server", server.Uuid),
					zap.Error(err),
				)
			}
		}
	}(s)
}
