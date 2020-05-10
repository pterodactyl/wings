package sftp

import (
	"github.com/pkg/errors"
	"github.com/pterodactyl/sftp-server"
	"github.com/pterodactyl/wings/api"
	"github.com/pterodactyl/wings/config"
	"github.com/pterodactyl/wings/server"
	"go.uber.org/zap"
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
		},
		CredentialValidator: validateCredentials,
		PathValidator: validatePath,
		DiskSpaceValidator: validateDiskSpace,
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

func validatePath(fs sftp_server.FileSystem, p string) (string, error) {
	s := server.GetServers().Find(func(server *server.Server) bool {
		return server.Uuid == fs.UUID
	})

	if s == nil {
		return "", errors.New("no server found with that UUID")
	}

	return s.Filesystem.SafePath(p)
}

func validateDiskSpace(fs sftp_server.FileSystem) bool {
	s := server.GetServers().Find(func(server *server.Server) bool {
		return server.Uuid == fs.UUID
	})

	if s == nil {
		return false
	}

	return s.Filesystem.HasSpaceAvailable()
}

// Validates a set of credentials for a SFTP login aganist Pterodactyl Panel and returns
// the server's UUID if the credentials were valid.
func validateCredentials(c sftp_server.AuthenticationRequest) (*sftp_server.AuthenticationResponse, error) {
	resp, err := api.NewRequester().ValidateSftpCredentials(c)
	zap.S().Named("sftp").Debugw("validating credentials for SFTP connection", zap.String("username", c.User))
	if err != nil {
		return resp, err
	}

	s := server.GetServers().Find(func(server *server.Server) bool {
		return server.Uuid == resp.Server
	})

	if s == nil {
		return resp, errors.New("no matching server with UUID found")
	}

	zap.S().Named("sftp").Debugw("matched user to server instance, credentials successfully validated", zap.String("username", c.User), zap.String("server", s.Uuid))
	return resp, err
}
