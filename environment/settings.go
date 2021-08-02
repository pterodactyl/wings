package environment

import (
	"fmt"
	"math"
	"strconv"

	"github.com/apex/log"
	"github.com/docker/docker/api/types/container"

	"github.com/pterodactyl/wings/config"
)

type Mount struct {
	// In Docker environments this makes no difference, however in a non-Docker environment you
	// should treat the "Default" mount as the root directory for the server. All other mounts
	// are just in addition to that one, and generally things like shared maps or timezone data.
	Default bool `json:"-"`

	// The target path on the system. This is "/home/container" for all server's Default mount
	// but in non-container environments you can likely ignore the target and just work with the
	// source.
	Target string `json:"target"`

	// The directory from which the files will be read. In Docker environments this is the directory
	// that we're mounting into the container at the Target location.
	Source string `json:"source"`

	// Whether the directory is being mounted as read-only. It is up to the environment to
	// handle this value correctly and ensure security expectations are met with its usage.
	ReadOnly bool `json:"read_only"`
}

// Limits is the build settings for a given server that impact docker container
// creation and resource limits for a server instance.
type Limits struct {
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

	OOMDisabled bool `json:"oom_disabled"`
}

// ConvertedCpuLimit converts the CPU limit for a server build into a number
// that can be better understood by the Docker environment. If there is no limit
// set, return -1 which will indicate to Docker that it has unlimited CPU quota.
func (l Limits) ConvertedCpuLimit() int64 {
	if l.CpuLimit == 0 {
		return -1
	}

	return l.CpuLimit * 1000
}

// MemoryOverheadMultiplier sets the hard limit for memory usage to be 5% more
// than the amount of memory assigned to the server. If the memory limit for the
// server is < 4G, use 10%, if less than 2G use 15%. This avoids unexpected
// crashes from processes like Java which run over the limit.
func (l Limits) MemoryOverheadMultiplier() float64 {
	if l.MemoryLimit <= 2048 {
		return 1.15
	} else if l.MemoryLimit <= 4096 {
		return 1.10
	}

	return 1.05
}

func (l Limits) BoundedMemoryLimit() int64 {
	return int64(math.Round(float64(l.MemoryLimit) * l.MemoryOverheadMultiplier() * 1_000_000))
}

// ConvertedSwap returns the amount of swap available as a total in bytes. This
// is returned as the amount of memory available to the server initially, PLUS
// the amount of additional swap to include which is the format used by Docker.
func (l Limits) ConvertedSwap() int64 {
	if l.Swap < 0 {
		return -1
	}

	return (l.Swap * 1_000_000) + l.BoundedMemoryLimit()
}

// ProcessLimit returns the process limit for a container. This is currently
// defined at a system level and not on a per-server basis.
func (l Limits) ProcessLimit() int64 {
	return config.Get().Docker.ContainerPidLimit
}

func (l Limits) AsContainerResources() container.Resources {
	pids := l.ProcessLimit()

	return container.Resources{
		Memory:            l.BoundedMemoryLimit(),
		MemoryReservation: l.MemoryLimit * 1_000_000,
		MemorySwap:        l.ConvertedSwap(),
		CPUQuota:          l.ConvertedCpuLimit(),
		CPUPeriod:         100_000,
		CPUShares:         1024,
		BlkioWeight:       l.IoWeight,
		OomKillDisable:    &l.OOMDisabled,
		CpusetCpus:        l.Threads,
		PidsLimit:         &pids,
	}
}

type Variables map[string]interface{}

// Get is an ugly hacky function to handle environment variables that get passed
// through as not-a-string from the Panel. Ideally we'd just say only pass
// strings, but that is a fragile idea and if a string wasn't passed through
// you'd cause a crash or the server to become unavailable. For now try to
// handle the most likely values from the JSON and hope for the best.
func (v Variables) Get(key string) string {
	val, ok := v[key]
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
	case string:
		return val.(string)
	}

	// TODO: I think we can add a check for val == nil and return an empty string for those
	//  and this warning should theoretically never happen?
	log.Warn(fmt.Sprintf("failed to marshal environment variable \"%s\" of type %+v into string", key, val))

	return ""
}
