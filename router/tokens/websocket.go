package tokens

import (
	"encoding/json"
	"github.com/gbrlsnchs/jwt/v3"
	"strings"
	"sync"
)

type WebsocketPayload struct {
	jwt.Payload
	sync.RWMutex

	UserID      json.Number `json:"user_id"`
	ServerUUID  string      `json:"server_uuid"`
	Permissions []string    `json:"permissions"`
}

// Returns the JWT payload.
func (p *WebsocketPayload) GetPayload() *jwt.Payload {
	p.RLock()
	defer p.RUnlock()

	return &p.Payload
}

func (p *WebsocketPayload) GetServerUuid() string {
	p.RLock()
	defer p.RUnlock()

	return p.ServerUUID
}

// Checks if the given token payload has a permission string.
func (p *WebsocketPayload) HasPermission(permission string) bool {
	p.RLock()
	defer p.RUnlock()

	for _, k := range p.Permissions {
		if k == permission || (!strings.HasPrefix(permission, "admin") && k == "*") {
			return true
		}
	}

	return false
}
