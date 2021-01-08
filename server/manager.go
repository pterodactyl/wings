package server

import (
	"github.com/pterodactyl/wings/panelapi"
)

type Manager interface {
	// Initialize fetches all servers assigned to this node from the API.
	Initialize(serversPerPage int) error
	GetAll() []*Server
	Get(uuid string) *Server
	Add(s *Server)
	Remove(s *Server)
}

type manager struct {
	servers Collection

	panelClient panelapi.Client
}

// NewManager creates a new server manager.
func NewManager(panelClient panelapi.Client) Manager {
	return &manager{panelClient: panelClient}
}

func (m *manager) GetAll() []*Server {
	return m.servers.items
}

func (m *manager) Get(uuid string) *Server {
	return m.servers.Find(func(s *Server) bool {
		return s.Id() == uuid
	})
}

func (m *manager) Add(s *Server) {
	s.manager = m
	m.servers.Add(s)
}

func (m *manager) Remove(s *Server) {
	m.servers.Remove(func(sf *Server) bool {
		return sf.Id() == s.Id()
	})
}
