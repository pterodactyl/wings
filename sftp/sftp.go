package sftp

import (
	"emperror.dev/errors"
	"github.com/apex/log"
	"github.com/pterodactyl/wings/api"
	"github.com/pterodactyl/wings/config"
	"github.com/pterodactyl/wings/server"
)

var noMatchingServerError = errors.New("no matching server with that UUID was found")

func Initialize(config config.SystemConfiguration) error {
	s := &SFTPServer{
		User: User{
			Uid: config.User.Uid,
			Gid: config.User.Gid,
		},
		Settings: Settings{
			BasePath:    config.Data,
			ReadOnly:    config.Sftp.ReadOnly,
			BindAddress: config.Sftp.Address,
			BindPort:    config.Sftp.Port,
		},
		credentialValidator: validateCredentials,
	}

	// Initialize the SFTP server in a background thread since this is
	// a long running operation.
	go func(s *SFTPServer) {
		if err := s.Initialize(); err != nil {
			log.WithField("subsystem", "sftp").WithField("error", err).Error("failed to initialize SFTP subsystem")
		}
	}(s)

	return nil
}

// Validates a set of credentials for a SFTP login against Pterodactyl Panel and returns
// the server's UUID if the credentials were valid.
func validateCredentials(c api.SftpAuthRequest) (*api.SftpAuthResponse, error) {
	f := log.Fields{"subsystem": "sftp", "username": c.User, "ip": c.IP}

	log.WithFields(f).Debug("validating credentials for SFTP connection")
	resp, err := api.New().ValidateSftpCredentials(c)
	if err != nil {
		if api.IsInvalidCredentialsError(err) {
			log.WithFields(f).Warn("failed to validate user credentials (invalid username or password)")
		} else {
			log.WithFields(f).Error("encountered an error while trying to validate user credentials")
		}

		return resp, err
	}

	s := server.GetServers().Find(func(server *server.Server) bool {
		return server.Id() == resp.Server
	})

	if s == nil {
		return resp, noMatchingServerError
	}

	s.Log().WithFields(f).Debug("credentials successfully validated and matched user to server instance")

	return resp, err
}
