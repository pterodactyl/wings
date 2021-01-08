package sftp

import (
	"time"

	"emperror.dev/errors"
	"github.com/apex/log"
	"github.com/patrickmn/go-cache"
	"github.com/pterodactyl/wings/api"
	"github.com/pterodactyl/wings/config"
)

var noMatchingServerError = errors.New("no matching server with that UUID was found")

func Initialize(config config.SystemConfiguration) error {
	s := &Server{
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
		cache: cache.New(5*time.Minute, 10*time.Minute),
	}
	s.CredentialValidator = s.validateCredentials
	s.PathValidator = s.validatePath
	s.DiskSpaceValidator = s.validateDiskSpace

	// Initialize the SFTP server in a background thread since this is
	// a long running operation.
	go func(s *Server) {
		if err := s.Initialize(); err != nil {
			log.WithField("subsystem", "sftp").WithField("error", err).Error("failed to initialize SFTP subsystem")
		}
	}(s)

	return nil
}

func (s *Server) validatePath(fs FileSystem, p string) (string, error) {
	srv := s.serverManager.Get(fs.UUID)
	if srv == nil {
		return "", noMatchingServerError
	}
	return srv.Filesystem().SafePath(p)
}

func (s *Server) validateDiskSpace(fs FileSystem) bool {
	srv := s.serverManager.Get(fs.UUID)
	if srv == nil {
		return false
	}
	return srv.Filesystem().HasSpaceAvailable(true)
}

// Validates a set of credentials for a SFTP login against Pterodactyl Panel and returns
// the server's UUID if the credentials were valid.
func (s *Server) validateCredentials(c api.SftpAuthRequest) (*api.SftpAuthResponse, error) {
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

	srv := s.serverManager.Get(resp.Server)
	if srv == nil {
		return resp, noMatchingServerError
	}

	srv.Log().WithFields(f).Debug("credentials successfully validated and matched user to server instance")

	return resp, err
}
