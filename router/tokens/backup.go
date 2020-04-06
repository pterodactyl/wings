package tokens

import (
	"github.com/gbrlsnchs/jwt/v3"
)

type BackupPayload struct {
	jwt.Payload
	ServerUuid string `json:"server_uuid"`
	BackupUuid string `json:"backup_uuid"`
	UniqueId   string `json:"unique_id"`
}

// Returns the JWT payload.
func (p *BackupPayload) GetPayload() *jwt.Payload {
	return &p.Payload
}

// Determines if this JWT is valid for the given request cycle. If the
// unique ID passed in the token has already been seen before this will
// return false. This allows us to use this JWT as a one-time token that
// validates all of the request.
func (p *BackupPayload) IsUniqueRequest() bool {
	return getTokenStore().IsValidToken(p.UniqueId)
}