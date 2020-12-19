package config

type ConsoleThrottles struct {
	// Whether or not the throttler is enabled for this instance.
	Enabled bool `json:"enabled" yaml:"enabled" default:"true"`

	// The total number of lines that can be output in a given LineResetInterval period before
	// a warning is triggered and counted against the server.
	Lines uint64 `json:"lines" yaml:"lines" default:"2000"`

	// The total number of throttle activations that can accumulate before a server is considered
	// to be breaching and will be stopped. This value is decremented by one every DecayInterval.
	MaximumTriggerCount uint64 `json:"maximum_trigger_count" yaml:"maximum_trigger_count" default:"5"`

	// The amount of time after which the number of lines processed is reset to 0. This runs in
	// a constant loop and is not affected by the current console output volumes. By default, this
	// will reset the processed line count back to 0 every 100ms.
	LineResetInterval uint64 `json:"line_reset_interval" yaml:"line_reset_interval" default:"100"`

	// The amount of time in milliseconds that must pass without an output warning being triggered
	// before a throttle activation is decremented.
	DecayInterval uint64 `json:"decay_interval" yaml:"decay_interval" default:"10000"`

	// The amount of time that a server is allowed to be stopping for before it is terminated
	// forcefully if it triggers output throttles.
	StopGracePeriod uint `json:"stop_grace_period" yaml:"stop_grace_period" default:"15"`
}
