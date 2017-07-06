package control

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
)

// Server is a single instance of a Service managed by the panel
type Server struct {
	// UUID is the unique identifier of the server
	UUID string `json:"uuid"`

	// Service is the service the server is an instance of
	Service *Service `json:"-"`

	// ServiceName is the name of the service. It is mainly used to allow storing the service
	// in the config
	ServiceName string `json:"serviceName"`

	Keys map[string][]string `json:"keys"`
}

var servers map[string]*Server

// LoadServerConfigurations loads the configured servers from a specified path
func LoadServerConfigurations(path string) error {
	serverFiles, err := ioutil.ReadDir(path)
	if err != nil {
		return err
	}
	servers = make(map[string]*Server)

	for _, file := range serverFiles {
		if !file.IsDir() {
			server, err := loadServerConfiguration(path + file.Name())
			if err != nil {
				return err
			}
			servers[server.UUID] = server
		}
	}

	return nil
}

func loadServerConfiguration(path string) (*Server, error) {
	file, err := ioutil.ReadFile(path)

	if err != nil {
		return nil, err
	}

	server := NewServer()
	if err := json.Unmarshal(file, server); err != nil {
		return nil, err
	}
	fmt.Println(server)
	return server, nil
}

// GetServer returns the server identified by the provided uuid
func GetServer(uuid string) *Server {
	return servers[uuid]
}

// NewServer creates a new Server
func NewServer() *Server {
	return new(Server)
}

// HasPermission checks wether a provided token has a specific permission
func (s *Server) HasPermission(token string, permission string) bool {
	for key, perms := range s.Keys {
		if key == token {
			for _, perm := range perms {
				if perm == permission || perm == "s:*" {
					return true
				}
			}
			return false
		}
	}
	return false
}
