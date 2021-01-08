package panelapi

import (
	"context"
	"errors"
	"regexp"

	"github.com/apex/log"
	"github.com/pterodactyl/wings/api"
)

// Usernames all follow the same format, so don't even bother hitting the API if the username is not
// at least in the expected format. This is very basic protection against random bots finding the SFTP
// server and sending a flood of usernames.
var validUsernameRegexp = regexp.MustCompile(`^(?i)(.+)\.([a-z0-9]{8})$`)

func (c *client) ValidateSftpCredentials(ctx context.Context, request api.SftpAuthRequest) (api.SftpAuthResponse, error) {
	if !validUsernameRegexp.MatchString(request.User) {
		log.WithFields(log.Fields{
			"subsystem": "sftp",
			"username":  request.User,
			"ip":        request.IP,
		}).Warn("failed to validate user credentials (invalid format)")
		return api.SftpAuthResponse{}, new(sftpInvalidCredentialsError)
	}

	res, err := c.post(ctx, "/sftp/auth", request)
	if err != nil {
		return api.SftpAuthResponse{}, err
	}

	e := res.Error()
	if e != nil {
		if res.StatusCode >= 400 && res.StatusCode < 500 {
			log.WithFields(log.Fields{
				"subsystem": "sftp",
				"username":  request.User,
				"ip":        request.IP,
			}).Warn(e.Error())

			return api.SftpAuthResponse{}, &sftpInvalidCredentialsError{}
		}

		return api.SftpAuthResponse{}, errors.New(e.Error())
	}

	r := api.SftpAuthResponse{}
	err = res.BindJSON(&r)
	return r, err
}
