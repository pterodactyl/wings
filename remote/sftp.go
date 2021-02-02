package remote

import (
	"context"
	"errors"

	"github.com/apex/log"
)

type SftpAuthRequest struct {
	User          string `json:"username"`
	Pass          string `json:"password"`
	IP            string `json:"ip"`
	SessionID     []byte `json:"session_id"`
	ClientVersion []byte `json:"client_version"`
}

type SftpAuthResponse struct {
	Server      string   `json:"server"`
	Token       string   `json:"token"`
	Permissions []string `json:"permissions"`
}

// ValidateSftpCredentials makes a request to determine if the username and
// password combination provided is associated with a valid server on the instance
// using the Panel's authentication control mechanisms. This will get itself
// throttled if too many requests are made, allowing us to completely offload
// all of the authorization security logic to the Panel.
func (c *client) ValidateSftpCredentials(ctx context.Context, request SftpAuthRequest) (SftpAuthResponse, error) {
	var auth SftpAuthResponse
	res, err := c.post(ctx, "/sftp/auth", request)
	if err != nil {
		return auth, err
	}

	e := res.Error()
	if e != nil {
		if res.StatusCode >= 400 && res.StatusCode < 500 {
			log.WithFields(log.Fields{
				"subsystem": "sftp",
				"username":  request.User,
				"ip":        request.IP,
			}).Warn(e.Error())

			return auth, &SftpInvalidCredentialsError{}
		}

		return auth, errors.New(e.Error())
	}

	err = res.BindJSON(&auth)
	return auth, err
}
