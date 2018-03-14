package control

import (
	"strings"

	"github.com/pterodactyl/wings/api/websockets"
)

func (s *ServerStruct) SetStatus(st Status) {
	s.status = st
	s.websockets.Broadcast <- websockets.Message{
		Type:    websockets.MessageTypeStatus,
		Payload: s.status,
	}
}

// Service returns the server's service configuration
func (s *ServerStruct) GetService() *Service {
	return s.Service
}

// UUIDShort returns the first block of the UUID
func (s *ServerStruct) UUIDShort() string {
	return s.ID[0:strings.Index(s.ID, "-")]
}

// Environment returns the servers environment
func (s *ServerStruct) Environment() (Environment, error) {
	return s.environment, nil
}

func (s *ServerStruct) Websockets() *websockets.Collection {
	return s.websockets
}

// HasPermission checks wether a provided token has a specific permission
func (s *ServerStruct) HasPermission(token string, permission string) bool {
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
