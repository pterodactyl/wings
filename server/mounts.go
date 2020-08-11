package server

import (
	"github.com/apex/log"
	"github.com/pkg/errors"
	"github.com/pterodactyl/wings/config"
	"github.com/pterodactyl/wings/environment"
	"os"
	"path/filepath"
	"strings"
)

// To avoid confusion when working with mounts, assume that a server.Mount has not been properly
// cleaned up and had the paths set. An environment.Mount should only be returned with valid paths
// that have been checked.
type Mount environment.Mount

// Returns the default container mounts for the server instance. This includes the data directory
// for the server as well as any timezone related files if they exist on the host system so that
// servers running within the container will use the correct time.
func (s *Server) Mounts() []environment.Mount {
	var m []environment.Mount

	m = append(m, environment.Mount{
		Default:  true,
		Target:   "/home/container",
		Source:   s.Filesystem.Path(),
		ReadOnly: false,
	})

	// Try to mount in /etc/localtime and /etc/timezone if they exist on the host system.
	if _, err := os.Stat("/etc/localtime"); err != nil {
		if !os.IsNotExist(err) {
			log.WithField("error", errors.WithStack(err)).Warn("failed to stat /etc/localtime due to an error")
		}
	} else {
		m = append(m, environment.Mount{
			Target:   "/etc/localtime",
			Source:   "/etc/localtime",
			ReadOnly: true,
		})
	}

	if _, err := os.Stat("/etc/timezone"); err != nil {
		if !os.IsNotExist(err) {
			log.WithField("error", errors.WithStack(err)).Warn("failed to stat /etc/timezone due to an error")
		}
	} else {
		m = append(m, environment.Mount{
			Target:   "/etc/timezone",
			Source:   "/etc/timezone",
			ReadOnly: true,
		})
	}

	// Also include any of this server's custom mounts when returning them.
	return append(m, s.customMounts()...)
}

// Returns the custom mounts for a given server after verifying that they are within a list of
// allowed mount points for the node.
func (s *Server) customMounts() []environment.Mount {
	var mounts []environment.Mount

	// TODO: probably need to handle things trying to mount directories that do not exist.
	for _, m := range s.Config().Mounts {
		source := filepath.Clean(m.Source)
		target := filepath.Clean(m.Target)

		logger := s.Log().WithFields(log.Fields{
			"source_path": source,
			"target_path": target,
			"read_only":   m.ReadOnly,
		})

		mounted := false
		for _, allowed := range config.Get().AllowedMounts {
			if !strings.HasPrefix(source, allowed) {
				continue
			}

			mounted = true
			mounts = append(mounts, environment.Mount{
				Source:   source,
				Target:   target,
				ReadOnly: m.ReadOnly,
			})

			break
		}

		if !mounted {
			logger.Warn("skipping custom server mount, not in list of allowed mount points")
		}
	}

	return mounts
}
