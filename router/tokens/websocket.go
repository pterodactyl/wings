package tokens

import (
	"encoding/json"
	"github.com/gbrlsnchs/jwt/v3"
)

type WebsocketPayload struct {
	jwt.Payload
	UserID      json.Number `json:"user_id"`
	ServerUUID  string      `json:"server_uuid"`
	Permissions []string    `json:"permissions"`
}

// Returns the JWT payload.
func (p *WebsocketPayload) GetPayload() *jwt.Payload {
	return &p.Payload
}

// Checks if the given token payload has a permission string.
func (p *WebsocketPayload) HasPermission(permission string) bool {
	for _, k := range p.Permissions {
		if k == permission {
			return true
		}
	}

	return false
}
