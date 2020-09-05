package config

type ConsoleThrottles struct {
	// Whether or not the throttler is enabled for this instance.
	Enabled bool `json:"enabled" yaml:"enabled" default:"true"`

	// The total number of throttle activations that must accumulate before a server is
	// forcibly stopped for violating these limits.
	KillAtCount uint64 `json:"kill_at_count" yaml:"kill_at_count" default:"5"`

	// The amount of time in milliseconds that a server process must go through without
	// triggering an output warning before the throttle activation count begins decreasing.
	// This time is measured in milliseconds.
	Decay uint64 `json:"decay" yaml:"decay" default:"10000"`

	// The total number of lines that can be output in a given CheckInterval period before
	// a warning is triggered and counted against the server.
	Lines uint64 `json:"lines" yaml:"lines" default:"1000"`

	// The amount of time that must pass between intervals before the count is reset. This
	// value is in milliseconds.
	CheckInterval uint64 `json:"check_interval" yaml:"check_interval" default:"100"`
}
