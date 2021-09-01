package server

import (
	"sync"

	"github.com/pterodactyl/wings/environment"
)

type EggConfiguration struct {
	// The internal UUID of the Egg on the Panel.
	ID string `json:"id"`

	// Maintains a list of files that are blacklisted for opening/editing/downloading
	// or basically any type of access on the server by any user. This is NOT the same
	// as a per-user denylist, this is defined at the Egg level.
	FileDenylist []string `json:"file_denylist"`
}

type Configuration struct {
	mu sync.RWMutex

	// The unique identifier for the server that should be used when referencing
	// it against the Panel API (and internally). This will be used when naming
	// docker containers as well as in log output.
	Uuid string `json:"uuid"`

	// Whether or not the server is in a suspended state. Suspended servers cannot
	// be started or modified except in certain scenarios by an admin user.
	Suspended bool `json:"suspended"`

	// The command that should be used when booting up the server instance.
	Invocation string `json:"invocation"`

	// By default this is false, however if selected within the Panel while installing or re-installing a
	// server, specific installation scripts will be skipped for the server process.
	SkipEggScripts bool `json:"skip_egg_scripts"`

	// An array of environment variables that should be passed along to the running
	// server process.
	EnvVars environment.Variables `json:"environment"`

	Allocations           environment.Allocations `json:"allocations"`
	Build                 environment.Limits      `json:"build"`
	CrashDetectionEnabled bool                    `json:"crash_detection_enabled"`
	Mounts                []Mount                 `json:"mounts"`
	Egg                   EggConfiguration        `json:"egg,omitempty"`

	Container struct {
		// Defines the Docker image that will be used for this server
		Image string `json:"image,omitempty"`
	} `json:"container,omitempty"`
}

func (s *Server) Config() *Configuration {
	s.cfg.mu.RLock()
	defer s.cfg.mu.RUnlock()
	return &s.cfg
}

// DiskSpace returns the amount of disk space available to a server in bytes.
func (s *Server) DiskSpace() int64 {
	s.cfg.mu.RLock()
	defer s.cfg.mu.RUnlock()
	return s.cfg.Build.DiskSpace * 1024.0 * 1024.0
}

func (s *Server) MemoryLimit() int64 {
	s.cfg.mu.RLock()
	defer s.cfg.mu.RUnlock()
	return s.cfg.Build.MemoryLimit
}

func (c *Configuration) GetUuid() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.Uuid
}

func (c *Configuration) SetSuspended(s bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.Suspended = s
}
