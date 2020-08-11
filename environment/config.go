package environment

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

type configurationSettings struct {
	Mounts      []Mount
	Allocations Allocations
	Limits      Limits
	Variables   Variables
}

// Defines the actual configuration struct for the environment with all of the settings
// defined within it.
type Configuration struct {
	mu sync.RWMutex

	settings configurationSettings
}

func NewConfiguration(m []Mount, a Allocations, l Limits, v Variables) *Configuration {
	return &Configuration{
		settings: configurationSettings{
			Mounts:      m,
			Allocations: a,
			Limits:      l,
			Variables:   v,
		},
	}
}

func (c *Configuration) Limits() Limits {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return c.settings.Limits
}

func (c *Configuration) Allocations() Allocations {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return c.settings.Allocations
}

func (c *Configuration) Mounts() []Mount {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return c.settings.Mounts
}

// Returns all of the environment variables that should be assigned to a running
// server instance.
func (c *Configuration) EnvironmentVariables(invocation string) []string {
	c.mu.RLock()
	c.mu.RUnlock()

	zone, _ := time.Now().In(time.Local).Zone()

	var out = []string{
		fmt.Sprintf("TZ=%s", zone),
		fmt.Sprintf("STARTUP=%s", invocation),
		fmt.Sprintf("SERVER_MEMORY=%d", c.settings.Limits.MemoryLimit),
		fmt.Sprintf("SERVER_IP=%s", c.settings.Allocations.DefaultMapping.Ip),
		fmt.Sprintf("SERVER_PORT=%d", c.settings.Allocations.DefaultMapping.Port),
	}

eloop:
	for k := range c.settings.Variables {
		for _, e := range out {
			if strings.HasPrefix(e, strings.ToUpper(k)) {
				continue eloop
			}
		}

		out = append(out, fmt.Sprintf("%s=%s", strings.ToUpper(k), c.settings.Variables.Get(k)))
	}

	return out
}
