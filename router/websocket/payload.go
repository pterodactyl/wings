package websocket

import (
	"encoding/json"
	"github.com/gbrlsnchs/jwt/v3"
)

type TokenPayload struct {
	jwt.Payload
	UserID      json.Number `json:"user_id"`
	ServerUUID  string      `json:"server_uuid"`
	Permissions []string    `json:"permissions"`
}

// Checks if the given token payload has a permission string.
func (p *TokenPayload) HasPermission(permission string) bool {
	for _, k := range p.Permissions {
		if k == permission {
			return true
		}
	}

	return false
}
