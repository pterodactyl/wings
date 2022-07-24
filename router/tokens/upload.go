package tokens

import (
	"github.com/gbrlsnchs/jwt/v3"
)

type UploadPayload struct {
	jwt.Payload

	ServerUuid string `json:"server_uuid"`
	UserUuid   string `json:"user_uuid"`
	UniqueId   string `json:"unique_id"`
}

// Returns the JWT payload.
func (p *UploadPayload) GetPayload() *jwt.Payload {
	return &p.Payload
}

// Determines if this JWT is valid for the given request cycle. If the
// unique ID passed in the token has already been seen before this will
// return false. This allows us to use this JWT as a one-time token that
// validates all of the request.
func (p *UploadPayload) IsUniqueRequest() bool {
	return getTokenStore().IsValidToken(p.UniqueId)
}
