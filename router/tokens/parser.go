package tokens

import (
	"github.com/gbrlsnchs/jwt/v3"
	"github.com/pterodactyl/wings/config"
	"time"
)

var alg *jwt.HMACSHA

type TokenData interface {
	GetPayload() *jwt.Payload
}

// Validates the provided JWT against the known secret for the Daemon and returns the
// parsed data. This function DOES NOT validate that the token is valid for the connected
// server, nor does it ensure that the user providing the token is able to actually do things.
//
// This simply returns a parsed token.
func ParseToken(token []byte, data TokenData) error {
	if alg == nil {
		alg = jwt.NewHS256([]byte(config.Get().AuthenticationToken))
	}

	verifyOptions := jwt.ValidatePayload(
		data.GetPayload(),
		jwt.ExpirationTimeValidator(time.Now()),
	)

	_, err := jwt.Verify(token, alg, &data, verifyOptions)

	return err
}