package server

import (
	"encoding/json"
	"github.com/pterodactyl/wings/environment"
	"sync"
)

// Defines the current resource usage for a given server instance. If a server is offline you
// should obviously expect memory and CPU usage to be 0. However, disk will always be returned
// since that is not dependent on the server being running to collect that data.
type ResourceUsage struct {
	mu sync.RWMutex

	// Embed the current environment stats into this server specific resource usage struct.
	environment.Stats

	// The current server status.
	State string `json:"state" default:"offline"`

	// The current disk space being used by the server. This value is not guaranteed to be accurate
	// at all times. It is "manually" set whenever server.Proc() is called. This is kind of just a
	// hacky solution for now to avoid passing events all over the place.
	Disk int64 `json:"disk_bytes"`
}

// Alias the resource usage so that we don't infinitely recurse when marshaling the struct.
type IResourceUsage ResourceUsage

// Custom marshaler to ensure that the object is locked when we're converting it to JSON in
// order to avoid race conditions.
func (ru *ResourceUsage) MarshalJSON() ([]byte, error) {
	ru.mu.Lock()
	defer ru.mu.Unlock()

	return json.Marshal(IResourceUsage(*ru))
}

// Returns the resource usage stats for the server instance. If the server is not running, only the
// disk space currently used will be returned. When the server is running all of the other stats will
// be returned.
//
// When a process is stopped all of the stats are zeroed out except for the disk.
func (s *Server) Proc() *ResourceUsage {
	s.resources.SetDisk(s.Filesystem().CachedUsage())

	// Get a read lock on the resources at this point. Don't do this before setting
	// the disk, otherwise you'll cause a deadlock.
	s.resources.mu.RLock()
	defer s.resources.mu.RUnlock()

	return &s.resources
}

func (s *Server) emitProcUsage() {
	if err := s.Events().PublishJson(StatsEvent, s.Proc()); err != nil {
		s.Log().WithField("error", err).Warn("error while emitting server resource usage to listeners")
	}
}

// Returns the servers current state.
func (ru *ResourceUsage) getInternalState() string {
	ru.mu.RLock()
	defer ru.mu.RUnlock()

	return ru.State
}

// Sets the new state for the server.
func (ru *ResourceUsage) setInternalState(state string) {
	ru.mu.Lock()
	ru.State = state
	ru.mu.Unlock()
}

func (ru *ResourceUsage) SetDisk(i int64) {
	ru.mu.Lock()
	ru.Disk = i
	ru.mu.Unlock()
}
