package control

// Server is a single instance of a Service managed by the panel
type Server struct {
	Service *Service
}

// HasPermission checks wether a provided token has a specific permission
func (s *Server) HasPermission(token string, permission string) bool {
	// TODO: properly implement this
	return true
}
