package server

import "github.com/pterodactyl/wings/api"

func (s *Server) ProcessConfiguration() *api.ProcessConfiguration {
	s.RLock()
	defer s.RUnlock()

	return s.procConfig
}
