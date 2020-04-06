package tokens

import (
	"github.com/gbrlsnchs/jwt/v3"
)

type TransferPayload struct {
	jwt.Payload
}

// GetPayload returns the JWT payload.
func (p *TransferPayload) GetPayload() *jwt.Payload {
	return &p.Payload
}
