package sftp

import (
	"github.com/pkg/errors"
	"github.com/pterodactyl/sftp-server"
	"github.com/pterodactyl/wings/api"
	"github.com/pterodactyl/wings/config"
	"go.uber.org/zap"
	"path"
)

func Initialize(config *config.Configuration) error {
	c := &sftp_server.Server{
		User: sftp_server.SftpUser{
			Uid: config.System.User.Uid,
			Gid: config.System.User.Gid,
		},
		Settings: sftp_server.Settings{
			BasePath:         config.System.Data,
			ReadOnly:         config.System.Sftp.ReadOnly,
			BindAddress:      config.System.Sftp.Address,
			BindPort:         config.System.Sftp.Port,
			ServerDataFolder: path.Join(config.System.Data, "/servers"),
			DisableDiskCheck: config.System.Sftp.DisableDiskChecking,
		},
		CredentialValidator: validateCredentials,
	}

	if err := sftp_server.New(c); err != nil {
		return err
	}

	c.ConfigureLogger(func() *zap.SugaredLogger {
		return zap.S().Named("sftp")
	})

	// Initialize the SFTP server in a background thread since this is
	// a long running operation.
	go func(instance *sftp_server.Server) {
		if err := c.Initalize(); err != nil {
			zap.S().Named("sftp").Errorw("failed to initialize SFTP subsystem", zap.Error(errors.WithStack(err)))
		}
	}(c)

	return nil
}

// Validates a set of credentials for a SFTP login aganist Pterodactyl Panel and returns
// the server's UUID if the credentials were valid.
func validateCredentials(c sftp_server.AuthenticationRequest) (*sftp_server.AuthenticationResponse, error) {
	return api.NewRequester().ValidateSftpCredentials(c)
}
