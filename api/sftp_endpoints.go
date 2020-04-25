package api

import (
	"encoding/json"
	"github.com/pkg/errors"
	"github.com/pterodactyl/sftp-server"
	"go.uber.org/zap"
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
		if r.HttpResponseCode() >= 400 && r.HttpResponseCode() < 500 {
			zap.S().Debugw("failed to validate server credentials for SFTP", zap.String("error", r.Error().String()))

			return nil, new(sftp_server.InvalidCredentialsError)
		}

		rerr := errors.New(r.Error().String())
		zap.S().Warnw("error validating SFTP credentials", zap.Error(rerr))

		return nil, rerr
	}

	response := new(sftp_server.AuthenticationResponse)
	body, _ := r.ReadBody()

	if err := json.Unmarshal(body, response); err != nil {
		return nil, err
	}

	return response, nil
}