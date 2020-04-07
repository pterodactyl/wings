package tokens

import (
	"encoding/json"
	"github.com/gbrlsnchs/jwt/v3"
	"strings"
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
		if k == permission || (!strings.HasPrefix(permission, "admin") && k == "*") {
			return true
		}
	}

	return false
}
