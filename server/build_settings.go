package server

import "math"

// The build settings for a given server that impact docker container creation and
// resource limits for a server instance.
type BuildSettings struct {
	// The total amount of memory in megabytes that this server is allowed to
	// use on the host system.
	MemoryLimit int64 `json:"memory_limit"`

	// The amount of additional swap space to be provided to a container instance.
	Swap int64 `json:"swap"`

	// The relative weight for IO operations in a container. This is relative to other
	// containers on the system and should be a value between 10 and 1000.
	IoWeight uint16 `json:"io_weight"`

	// The percentage of CPU that this instance is allowed to consume relative to
	// the host. A value of 200% represents complete utilization of two cores. This
	// should be a value between 1 and THREAD_COUNT * 100.
	CpuLimit int64 `json:"cpu_limit"`

	// The amount of disk space in megabytes that a server is allowed to use.
	DiskSpace int64 `json:"disk_space"`

	// Sets which CPU threads can be used by the docker instance.
	Threads string `json:"threads"`
}

func (s *Server) Build() *BuildSettings {
	return &s.Config().Build
}

// Converts the CPU limit for a server build into a number that can be better understood
// by the Docker environment. If there is no limit set, return -1 which will indicate to
// Docker that it has unlimited CPU quota.
func (b *BuildSettings) ConvertedCpuLimit() int64 {
	if b.CpuLimit == 0 {
		return -1
	}

	return b.CpuLimit * 1000
}

// Set the hard limit for memory usage to be 5% more than the amount of memory assigned to
// the server. If the memory limit for the server is < 4G, use 10%, if less than 2G use
// 15%. This avoids unexpected crashes from processes like Java which run over the limit.
func (b *BuildSettings) MemoryOverheadMultiplier() float64 {
	if b.MemoryLimit <= 2048 {
		return 1.15
	} else if b.MemoryLimit <= 4096 {
		return 1.10
	}

	return 1.05
}

func (b *BuildSettings) BoundedMemoryLimit() int64 {
	return int64(math.Round(float64(b.MemoryLimit) * b.MemoryOverheadMultiplier() * 1_000_000))
}

// Returns the amount of swap available as a total in bytes. This is returned as the amount
// of memory available to the server initially, PLUS the amount of additional swap to include
// which is the format used by Docker.
func (b *BuildSettings) ConvertedSwap() int64 {
	if b.Swap < 0 {
		return -1
	}

	return (b.Swap * 1_000_000) + b.BoundedMemoryLimit()
}
