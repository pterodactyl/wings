package server

import (
	"sync"
	"sync/atomic"

	"github.com/pterodactyl/wings/environment"
	"github.com/pterodactyl/wings/system"
)

// ResourceUsage defines the current resource usage for a given server instance. If a server is offline you
// should obviously expect memory and CPU usage to be 0. However, disk will always be returned
// since that is not dependent on the server being running to collect that data.
type ResourceUsage struct {
	mu sync.RWMutex

	// Embed the current environment stats into this server specific resource usage struct.
	environment.Stats

	// The current server status.
	State *system.AtomicString `json:"state"`

	// The current disk space being used by the server. This value is not guaranteed to be accurate
	// at all times. It is "manually" set whenever server.Proc() is called. This is kind of just a
	// hacky solution for now to avoid passing events all over the place.
	Disk int64 `json:"disk_bytes"`
}

// Proc returns the current resource usage stats for the server instance. This returns
// a copy of the tracked resources, so making any changes to the response will not
// have the desired outcome for you most likely.
func (s *Server) Proc() ResourceUsage {
	s.resources.mu.Lock()
	defer s.resources.mu.Unlock()
	// Store the updated disk usage when requesting process usage.
	atomic.StoreInt64(&s.resources.Disk, s.Filesystem().CachedUsage())
	//goland:noinspection GoVetCopyLock
	return s.resources
}

// Reset resets the usages values to zero, used when a server is stopped to ensure we don't hold
// onto any values incorrectly.
func (ru *ResourceUsage) Reset() {
	ru.mu.Lock()
	defer ru.mu.Unlock()

	ru.Memory = 0
	ru.CpuAbsolute = 0
	ru.Uptime = 0
	ru.Network.TxBytes = 0
	ru.Network.RxBytes = 0
}

func (s *Server) emitProcUsage() {
	if err := s.Events().PublishJson(StatsEvent, s.Proc()); err != nil {
		s.Log().WithField("error", err).Warn("error while emitting server resource usage to listeners")
	}
}
