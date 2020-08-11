package server

import (
	"github.com/apex/log"
	"github.com/docker/docker/api/types/mount"
	"github.com/pterodactyl/wings/config"
	"os"
	"path/filepath"
	"strings"
)

// Returns the default container mounts for the server instance. This includes the data directory
// for the server as well as any timezone related files if they exist on the host system so that
// servers running within the container will use the correct time.
func (s *Server) Mounts() ([]mount.Mount, error) {
	var m []mount.Mount

	m = append(m, mount.Mount{
		Target:   "/home/container",
		Source:   s.Filesystem.Path(),
		Type:     mount.TypeBind,
		ReadOnly: false,
	})

	// Try to mount in /etc/localtime and /etc/timezone if they exist on the host system.
	if _, err := os.Stat("/etc/localtime"); err != nil {
		if !os.IsNotExist(err) {
			return nil, err
		}
	} else {
		m = append(m, mount.Mount{
			Target:   "/etc/localtime",
			Source:   "/etc/localtime",
			Type:     mount.TypeBind,
			ReadOnly: true,
		})
	}

	if _, err := os.Stat("/etc/timezone"); err != nil {
		if !os.IsNotExist(err) {
			return nil, err
		}
	} else {
		m = append(m, mount.Mount{
			Target:   "/etc/timezone",
			Source:   "/etc/timezone",
			Type:     mount.TypeBind,
			ReadOnly: true,
		})
	}

	return m, nil
}

// Returns the custom mounts for a given server after verifying that they are within a list of
// allowed mount points for the node.
func (s *Server) CustomMounts() ([]mount.Mount, error) {
	var mounts []mount.Mount

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
			mounts = append(mounts, mount.Mount{
				Source:   source,
				Target:   target,
				Type:     mount.TypeBind,
				ReadOnly: m.ReadOnly,
			})

			break
		}

		if !mounted {
			logger.Warn("skipping custom server mount, not in list of allowed mount points")
		}
	}

	return mounts, nil
}
