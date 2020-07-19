package server

import (
	"fmt"
	"strconv"
	"sync"
)

type EnvironmentVariables map[string]interface{}

// Ugly hacky function to handle environment variables that get passed through as not-a-string
// from the Panel. Ideally we'd just say only pass strings, but that is a fragile idea and if a
// string wasn't passed through you'd cause a crash or the server to become unavailable. For now
// try to handle the most likely values from the JSON and hope for the best.
func (ev EnvironmentVariables) Get(key string) string {
	val, ok := ev[key]
	if !ok {
		return ""
	}

	switch val.(type) {
	case int:
		return strconv.Itoa(val.(int))
	case int32:
		return strconv.FormatInt(val.(int64), 10)
	case int64:
		return strconv.FormatInt(val.(int64), 10)
	case float32:
		return fmt.Sprintf("%f", val.(float32))
	case float64:
		return fmt.Sprintf("%f", val.(float64))
	case bool:
		return strconv.FormatBool(val.(bool))
	}

	return val.(string)
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

	// An array of environment variables that should be passed along to the running
	// server process.
	EnvVars EnvironmentVariables `json:"environment"`

	Allocations           Allocations   `json:"allocations"`
	Build                 BuildSettings `json:"build"`
	CrashDetectionEnabled bool          `default:"true" json:"enabled" yaml:"enabled"`
	Mounts                []Mount       `json:"mounts"`
	Resources             ResourceUsage `json:"resources"`

	Container struct {
		// Defines the Docker image that will be used for this server
		Image string `json:"image,omitempty"`
		// If set to true, OOM killer will be disabled on the server's Docker container.
		// If not present (nil) we will default to disabling it.
		OomDisabled bool `default:"true" json:"oom_disabled"`
	} `json:"container,omitempty"`
}

func (s *Server) Config() *Configuration {
	s.cfg.mu.RLock()
	defer s.cfg.mu.RUnlock()

	return &s.cfg
}

func (c *Configuration) SetSuspended(s bool) {
	c.mu.Lock()
	c.Suspended = s
	c.mu.Unlock()
}
