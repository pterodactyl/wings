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

	// The current disk space being used by the server. This is cached to prevent slow lookup
	// issues on frequent refreshes.
	Disk int64 `json:"disk_bytes"`
}

// Returns the resource usage stats for the server instance. If the server is not running, only the
// disk space currently used will be returned. When the server is running all of the other stats will
// be returned.
//
// When a process is stopped all of the stats are zeroed out except for the disk.
func (s *Server) Proc() *ResourceUsage {
	s.resources.mu.RLock()
	defer s.resources.mu.RUnlock()

	return &s.resources
}

func (s *Server) emitProcUsage() {
	s.resources.mu.RLock()
	defer s.resources.mu.RUnlock()

	b, err := json.Marshal(s.resources)
	if err == nil {
		s.Events().Publish(StatsEvent, string(b))
	}

	// TODO: This might be a good place to add a debug log if stats are not sending.
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
