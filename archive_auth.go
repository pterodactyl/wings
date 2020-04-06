package main

import (
	"github.com/gbrlsnchs/jwt/v3"
	"github.com/pterodactyl/wings/config"
	"time"
)

var alg *jwt.HMACSHA

// ArchiveTokenPayload represents an Archive Token Payload.
type ArchiveTokenPayload struct {
	jwt.Payload
}

func ParseArchiveJWT(token []byte) (*ArchiveTokenPayload, error) {
	var payload ArchiveTokenPayload
	if alg == nil {
		alg = jwt.NewHS256([]byte(config.Get().AuthenticationToken))
	}

	now := time.Now()
	verifyOptions := jwt.ValidatePayload(
		&payload.Payload,
		jwt.ExpirationTimeValidator(now),
	)

	_, err := jwt.Verify(token, alg, &payload, verifyOptions)
	if err != nil {
		return nil, err
	}

	return &payload, nil
}
