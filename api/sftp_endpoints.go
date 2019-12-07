package api

import (
	"encoding/json"
	"github.com/pkg/errors"
	"github.com/pterodactyl/sftp-server"
)

func (r *PanelRequest) ValidateSftpCredentials(request sftp_server.AuthenticationRequest) (*sftp_server.AuthenticationResponse, error) {
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
		if r.HttpResponseCode() == 403 {
			return nil, sftp_server.InvalidCredentialsError{}
		}

		return nil, errors.WithStack(errors.New(r.Error()))
	}

	response := new(sftp_server.AuthenticationResponse)
	body, _ := r.ReadBody()

	if err := json.Unmarshal(body, response); err != nil {
		return nil, err
	}

	return response, nil
}