package server

import (
	"emperror.dev/errors"
	"encoding/json"
	"github.com/buger/jsonparser"
	"github.com/imdario/mergo"
	"github.com/pterodactyl/wings/environment"
)

// Merges data passed through in JSON form into the existing server object.
// Any changes to the build settings will apply immediately in the environment
// if the environment supports it.
//
// The server will be marked as requiring a rebuild on the next boot sequence,
// it is up to the specific environment to determine what needs to happen when
// that is the case.
func (s *Server) UpdateDataStructure(data []byte) error {
	src := new(Configuration)
	if err := json.Unmarshal(data, src); err != nil {
		return err
	}

	// Don't allow obviously corrupted data to pass through into this function. If the UUID
	// doesn't match something has gone wrong and the API is attempting to meld this server
	// instance into a totally different one, which would be bad.
	if src.Uuid != "" && s.Id() != "" && src.Uuid != s.Id() {
		return errors.New("attempting to merge a data stack with an invalid UUID")
	}

	// Grab a copy of the configuration to work on.
	c := *s.Config()

	// Lock our copy of the configuration since the deferred unlock will end up acting upon this
	// new memory address rather than the old one. If we don't lock this, the deferred unlock will
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
		return err
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
			return err
		}
	} else {
		c.Build.OOMDisabled = v
	}

	// Mergo also cannot handle this boolean value.
	if v, err := jsonparser.GetBoolean(data, "suspended"); err != nil {
		if err != jsonparser.KeyPathNotFoundError {
			return err
		}
	} else {
		c.Suspended = v
	}

	if v, err := jsonparser.GetBoolean(data, "skip_egg_scripts"); err != nil {
		if err != jsonparser.KeyPathNotFoundError {
			return err
		}
	} else {
		c.SkipEggScripts = v
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

	return nil
}

// Updates the environment for the server to match any of the changed data. This pushes new settings and
// environment variables to the environment. In addition, the in-situ update method is called on the
// environment which will allow environments that make use of it (such as Docker) to immediately apply
// some settings without having to wait on a server to restart.
//
// This functionality allows a server's resources limits to be modified on the fly and have them apply
// right away allowing for dynamic resource allocation and responses to abusive server processes.
func (s *Server) SyncWithEnvironment() {
	s.Log().Debug("syncing server settings with environment")

	// Update the environment settings using the new information from this server.
	s.Environment.Config().SetSettings(environment.Settings{
		Mounts:      s.Mounts(),
		Allocations: s.Config().Allocations,
		Limits:      s.Config().Build,
	})

	// If build limits are changed, environment variables also change. Plus, any modifications to
	// the startup command also need to be properly propagated to this environment.
	//
	// @see https://github.com/pterodactyl/panel/issues/2255
	s.Environment.Config().SetEnvironmentVariables(s.GetEnvironmentVariables())

	if !s.IsSuspended() {
		// Update the environment in place, allowing memory and CPU usage to be adjusted
		// on the fly without the user needing to reboot (theoretically).
		s.Log().Info("performing server limit modification on-the-fly")
		if err := s.Environment.InSituUpdate(); err != nil {
			// This is not a failure, the process is still running fine and will fix itself on the
			// next boot, or fail out entirely in a more logical position.
			s.Log().WithField("error", err).Warn("failed to perform on-the-fly update of the server environment")
		}
	} else {
		// Checks if the server is now in a suspended state. If so and a server process is currently running it
		// will be gracefully stopped (and terminated if it refuses to stop).
		if s.Environment.State() != environment.ProcessOfflineState {
			s.Log().Info("server suspended with running process state, terminating now")

			go func(s *Server) {
				if err := s.Environment.WaitForStop(60, true); err != nil {
					s.Log().WithField("error", err).Warn("failed to terminate server environment after suspension")
				}
			}(s)
		}
	}
}
