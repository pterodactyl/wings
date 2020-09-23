package api

import (
	"encoding/json"
	"github.com/apex/log"
	"github.com/pkg/errors"
	"regexp"
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

type sftpInvalidCredentialsError struct {
}

func (ice sftpInvalidCredentialsError) Error() string {
	return "the credentials provided were invalid"
}

func IsInvalidCredentialsError(err error) bool {
	_, ok := err.(*sftpInvalidCredentialsError)

	return ok
}

// Usernames all follow the same format, so don't even bother hitting the API if the username is not
// at least in the expected format. This is very basic protection against random bots finding the SFTP
// server and sending a flood of usernames.
var validUsernameRegexp = regexp.MustCompile(`^(?i)(.+)\.([a-z0-9]{8})$`)

func (r *PanelRequest) ValidateSftpCredentials(request SftpAuthRequest) (*SftpAuthResponse, error) {
	// If the username doesn't meet the expected format that the Panel would even recognize just go ahead
	// and bail out of the process here to avoid accidentally brute forcing the panel if a bot decides
	// to connect to spam username attempts.
	if !validUsernameRegexp.MatchString(request.User) {
		log.WithFields(log.Fields{
			"subsystem": "sftp",
			"username":  request.User,
			"ip":        request.IP,
		}).Warn("failed to validate user credentials (invalid format)")

		return nil, new(sftpInvalidCredentialsError)
	}

	b, err := json.Marshal(request)
	if err != nil {
		return nil, err
	}

	resp, err := r.Post("/sftp/auth", b)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	r.Response = resp

	if r.HasError() {
		if r.HttpResponseCode() >= 400 && r.HttpResponseCode() < 500 {
			log.WithFields(log.Fields{
				"subsystem": "sftp",
				"username":  request.User,
				"ip":        request.IP,
			}).Warn(r.Error().String())

			return nil, new(sftpInvalidCredentialsError)
		}

		rerr := errors.New(r.Error().String())

		return nil, rerr
	}

	response := new(SftpAuthResponse)
	body, _ := r.ReadBody()

	if err := json.Unmarshal(body, response); err != nil {
		return nil, err
	}

	return response, nil
}
