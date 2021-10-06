package environment

// Stats defines the current resource usage for a given server instance.
type Stats struct {
	// The total amount of memory, in bytes, that this server instance is consuming. This is
	// calculated slightly differently than just using the raw Memory field that the stats
	// return from the container, so please check the code setting this value for how that
	// is calculated.
	Memory uint64 `json:"memory_bytes"`

	// The total amount of memory this container or resource can use. Inside Docker this is
	// going to be higher than you'd expect because we're automatically allocating overhead
	// abilities for the container, so it's not going to be a perfect match.
	MemoryLimit uint64 `json:"memory_limit_bytes"`

	// The absolute CPU usage is the amount of CPU used in relation to the entire system and
	// does not take into account any limits on the server process itself.
	CpuAbsolute float64 `json:"cpu_absolute"`

	// Current network transmit in & out for a container.
	Network NetworkStats `json:"network"`

	// The current uptime of the container, in milliseconds.
	Uptime int64 `json:"uptime"`
}

type NetworkStats struct {
	RxBytes uint64 `json:"rx_bytes"`
	TxBytes uint64 `json:"tx_bytes"`
}
