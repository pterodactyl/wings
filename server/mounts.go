package server

import (
	"path/filepath"
	"strings"

	"github.com/apex/log"

	"github.com/pterodactyl/wings/config"
	"github.com/pterodactyl/wings/environment"
)

// To avoid confusion when working with mounts, assume that a server.Mount has not been properly
// cleaned up and had the paths set. An environment.Mount should only be returned with valid paths
// that have been checked.
type Mount environment.Mount

// Returns the default container mounts for the server instance. This includes the data directory
// for the server. Previously this would also mount in host timezone files, however we've moved from
// that approach to just setting `TZ=Timezone` environment values in containers which should work
// in most scenarios.
func (s *Server) Mounts() []environment.Mount {
	m := []environment.Mount{
		{
			Default:  true,
			Target:   "/home/container",
			Source:   s.Filesystem().Path(),
			ReadOnly: false,
		},
	}

	cfg := config.Get()

	// Handle mounting a generated `/etc/passwd` if the feature is enabled.
	if cfg.System.Passwd.Enable {
		s.Log().WithFields(log.Fields{"source_path": cfg.System.Passwd.Directory}).Info("mouting generated /etc/{group,passwd} to workaround UID/GID issues")
		m = append(m, environment.Mount{
			Source:   filepath.Join(cfg.System.Passwd.Directory, "group"),
			Target:   "/etc/group",
			ReadOnly: true,
		})
		m = append(m, environment.Mount{
			Source:   filepath.Join(cfg.System.Passwd.Directory, "passwd"),
			Target:   "/etc/passwd",
			ReadOnly: true,
		})
	}

	if cfg.System.MachineID.Enable {
		// Hytale wants a machine-id in order to encrypt tokens for the server.
		// So add a mount to `/etc/machine-id` to a source that contains the
		// server's UUID without any dashes.
		m = append(m, environment.Mount{
			Source:   filepath.Join(cfg.System.MachineID.Directory, s.ID()),
			Target:   "/etc/machine-id",
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
			// Check if the source path is included in the allowed mounts list.
			// filepath.Clean will strip all trailing slashes (unless the path is a root directory).
			if !strings.HasPrefix(source, filepath.Clean(allowed)) {
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
