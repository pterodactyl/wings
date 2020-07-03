package sftp

import (
	"github.com/apex/log"
	"github.com/pkg/errors"
	"github.com/pterodactyl/sftp-server"
	"github.com/pterodactyl/wings/api"
	"github.com/pterodactyl/wings/config"
	"github.com/pterodactyl/wings/server"
	"go.uber.org/zap"
	"regexp"
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
			log.WithField("subsystem", "sftp").WithField("error", errors.WithStack(err)).Error("failed to initialize SFTP subsystem")
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

var validUsernameRegexp = regexp.MustCompile(`^(?i)(.+)\.([a-z0-9]{8})$`)

// Validates a set of credentials for a SFTP login aganist Pterodactyl Panel and returns
// the server's UUID if the credentials were valid.
func validateCredentials(c sftp_server.AuthenticationRequest) (*sftp_server.AuthenticationResponse, error) {
	log.WithFields(log.Fields{"subsystem": "sftp", "username": c.User}).Debug("validating credentials for SFTP connection")

	f := log.Fields{
		"subsystem": "sftp",
		"username":  c.User,
		"ip":        c.IP,
	}

	// If the username doesn't meet the expected format that the Panel would even recognize just go ahead
	// and bail out of the process here to avoid accidentially brute forcing the panel if a bot decides
	// to connect to spam username attempts.
	if !validUsernameRegexp.MatchString(c.User) {
		log.WithFields(f).Warn("failed to validate user credentials (invalid format)")

		return nil, new(sftp_server.InvalidCredentialsError)
	}

	resp, err := api.NewRequester().ValidateSftpCredentials(c)
	if err != nil {
		if sftp_server.IsInvalidCredentialsError(err) {
			log.WithFields(f).Warn("failed to validate user credentials (invalid username or password)")
		} else {
			log.WithFields(f).Error("encountered an error while trying to validate user credentials")
		}

		return resp, err
	}

	s := server.GetServers().Find(func(server *server.Server) bool {
		return server.Uuid == resp.Server
	})

	if s == nil {
		return resp, errors.New("no matching server with UUID found")
	}

	s.Log().WithFields(f).Debug("credentials successfully validated and matched user to server instance")

	return resp, err
}
