package environment

import (
	"sync"
)

type configurationSettings struct {
	Mounts      []Mount
	Allocations Allocations
	Limits      Limits
}

// Defines the actual configuration struct for the environment with all of the settings
// defined within it.
type Configuration struct {
	mu sync.RWMutex

	environmentVariables []string
	settings             configurationSettings
}

func NewConfiguration(m []Mount, a Allocations, l Limits, envVars []string) *Configuration {
	return &Configuration{
		environmentVariables: envVars,
		settings: configurationSettings{
			Mounts:      m,
			Allocations: a,
			Limits:      l,
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

func (c *Configuration) EnvironmentVariables() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return c.environmentVariables
}