package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"runtime"
	"sync"
	"time"

	"emperror.dev/errors"
	"github.com/apex/log"
	"github.com/gammazero/workerpool"
	"github.com/pterodactyl/wings/api"
	"github.com/pterodactyl/wings/config"
	"github.com/pterodactyl/wings/remote"
)

type Manager struct {
	mu      sync.RWMutex
	servers []*Server
}

// NewManager returns a new server manager instance. This will boot up all of
// the servers that are currently present on the filesystem and set them into
// the manager.
func NewManager(ctx context.Context, client remote.Client) (*Manager, error) {
	c := NewEmptyManager()
	if err := c.initializeFromRemoteSource(ctx, client); err != nil {
		return nil, err
	}
	return c, nil
}

// NewEmptyManager returns a new empty manager collection without actually
// loading any of the servers from the disk. This allows the caller to set their
// own servers into the collection as needed.
func NewEmptyManager() *Manager {
	return &Manager{}
}

// initializeFromRemoteSource iterates over a given directory and loads all of
// the servers listed before returning them to the calling function.
func (m *Manager) initializeFromRemoteSource(ctx context.Context, client remote.Client) error {
	log.Info("fetching list of servers from API")
	servers, err := client.GetServers(ctx, config.Get().RemoteQuery.BootServersPerPage)
	if err != nil {
		if !remote.IsRequestError(err) {
			return errors.WithStackIf(err)
		}
		return errors.New(err.Error())
	}

	start := time.Now()
	log.WithField("total_configs", len(servers)).Info("processing servers returned by the API")

	pool := workerpool.New(runtime.NumCPU())
	log.Debugf("using %d workerpools to instantiate server instances", runtime.NumCPU())
	for _, data := range servers {
		pool.Submit(func() {
			// Parse the json.RawMessage into an expected struct value. We do this here so that a single broken
			// server does not cause the entire boot process to hang, and allows us to show more useful error
			// messaging in the output.
			d := api.ServerConfigurationResponse{
				Settings: data.Settings,
			}
			log.WithField("server", data.Uuid).Info("creating new server object from API response")
			if err := json.Unmarshal(data.ProcessConfiguration, &d.ProcessConfiguration); err != nil {
				log.WithField("server", data.Uuid).WithField("error", err).Error("failed to parse server configuration from API response, skipping...")
				return
			}
			s, err := FromConfiguration(d)
			if err != nil {
				log.WithField("server", data.Uuid).WithField("error", err).Error("failed to load server, skipping...")
				return
			}
			m.Add(s)
		})
	}

	// Wait until we've processed all of the configuration files in the directory
	// before continuing.
	pool.StopWait()

	diff := time.Now().Sub(start)
	log.WithField("duration", fmt.Sprintf("%s", diff)).Info("finished processing server configurations")

	return nil
}

// Put replaces all of the current values in the collection with the value that
// is passed through.
func (m *Manager) Put(s []*Server) {
	m.mu.Lock()
	m.servers = s
	m.mu.Unlock()
}

// All returns all of the items in the collection.
func (m *Manager) All() []*Server {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.servers
}

// Add adds an item to the collection store.
func (m *Manager) Add(s *Server) {
	m.mu.Lock()
	m.servers = append(m.servers, s)
	m.mu.Unlock()
}

// Get returns a single server instance and a boolean value indicating if it was
// found in the global collection or not.
func (m *Manager) Get(uuid string) (*Server, bool) {
	match := m.Find(func(server *Server) bool {
		return server.Id() == uuid
	})
	return match, match != nil
}

// Filter returns only those items matching the filter criteria.
func (m *Manager) Filter(filter func(match *Server) bool) []*Server {
	m.mu.RLock()
	defer m.mu.RUnlock()
	r := make([]*Server, 0)
	for _, v := range m.servers {
		if filter(v) {
			r = append(r, v)
		}
	}
	return r
}

// Find returns a single element from the collection matching the filter. If
// nothing is found a nil result is returned.
func (m *Manager) Find(filter func(match *Server) bool) *Server {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, v := range m.servers {
		if filter(v) {
			return v
		}
	}
	return nil
}

// Remove removes all items from the collection that match the filter function.
func (m *Manager) Remove(filter func(match *Server) bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r := make([]*Server, 0)
	for _, v := range m.servers {
		if !filter(v) {
			r = append(r, v)
		}
	}
	m.servers = r
}

// PersistStates writes the current environment states to the disk for each
// server. This is generally called at a specific interval defined in the root
// runner command to avoid hammering disk I/O when tons of server switch states
// at once. It is fine if this file falls slightly out of sync, it is just here
// to make recovering from an unexpected system reboot a little easier.
func (m *Manager) PersistStates() error {
	states := map[string]string{}
	for _, s := range m.All() {
		states[s.Id()] = s.Environment.State()
	}
	data, err := json.Marshal(states)
	if err != nil {
		return errors.WithStack(err)
	}
	if err := ioutil.WriteFile(config.Get().System.GetStatesPath(), data, 0644); err != nil {
		return errors.WithStack(err)
	}
	return nil
}

// ReadStates returns the state of the servers.
func (m *Manager) ReadStates() (map[string]string, error) {
	f, err := os.OpenFile(config.Get().System.GetStatesPath(), os.O_RDONLY|os.O_CREATE, 0644)
	if err != nil {
		return nil, errors.WithStack(err)
	}
	defer f.Close()
	var states map[string]string
	if err := json.NewDecoder(f).Decode(&states); err != nil && err != io.EOF {
		return nil, errors.WithStack(err)
	}
	out := make(map[string]string, 0)
	// Only return states for servers that we're currently tracking in the system.
	for id, state := range states {
		if _, ok := m.Get(id); ok {
			out[id] = state
		}
	}
	return out, nil
}