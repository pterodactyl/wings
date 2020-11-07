package server

import (
	"encoding/json"
	"github.com/pterodactyl/wings/environment"
	"github.com/pterodactyl/wings/system"
	"sync"
	"sync/atomic"
)

// Defines the current resource usage for a given server instance. If a server is offline you
// should obviously expect memory and CPU usage to be 0. However, disk will always be returned
// since that is not dependent on the server being running to collect that data.
type ResourceUsage struct {
	mu sync.RWMutex

	// Embed the current environment stats into this server specific resource usage struct.
	environment.Stats

	// The current server status.
	State system.AtomicString `json:"state"`

	// The current disk space being used by the server. This value is not guaranteed to be accurate
	// at all times. It is "manually" set whenever server.Proc() is called. This is kind of just a
	// hacky solution for now to avoid passing events all over the place.
	Disk int64 `json:"disk_bytes"`
}

// Custom marshaler to ensure that the object is locked when we're converting it to JSON in
// order to avoid race conditions.
func (ru *ResourceUsage) MarshalJSON() ([]byte, error) {
	ru.mu.Lock()
	defer ru.mu.Unlock()

	// Alias the resource usage so that we don't infinitely recurse when marshaling the struct.
	type alias ResourceUsage

	return json.Marshal(alias(*ru))
}

// Returns the resource usage stats for the server instance. If the server is not running, only the
// disk space currently used will be returned. When the server is running all of the other stats will
// be returned.
//
// When a process is stopped all of the stats are zeroed out except for the disk.
func (s *Server) Proc() *ResourceUsage {
	// Store the updated disk usage when requesting process usage.
	atomic.StoreInt64(&s.resources.Disk, s.Filesystem().CachedUsage())

	// Acquire a lock before attempting to return the value of resources.
	s.resources.mu.RLock()
	defer s.resources.mu.RUnlock()

	return &s.resources
}

func (s *Server) emitProcUsage() {
	if err := s.Events().PublishJson(StatsEvent, s.Proc()); err != nil {
		s.Log().WithField("error", err).Warn("error while emitting server resource usage to listeners")
	}
}
