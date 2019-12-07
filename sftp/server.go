package sftp

import (
	"github.com/patrickmn/go-cache"
	sftpserver "github.com/pterodactyl/sftp-server/src/server"
	"github.com/pterodactyl/wings/config"
	"path"
	"time"
)

func Initialize(config *config.Configuration) error {
	c := sftpserver.Configuration{
		Data:  []byte("{}"),
		Cache: cache.New(5*time.Minute, 10*time.Minute),
		User: sftpserver.SftpUser{
			Uid: config.System.User.Uid,
			Gid: config.System.User.Gid,
		},
		Settings: sftpserver.Settings{
			BasePath:         config.System.Data,
			ReadOnly:         config.System.Sftp.ReadOnly,
			BindAddress:      config.System.Sftp.Address,
			BindPort:         config.System.Sftp.Port,
			ServerDataFolder: path.Join(config.System.Data, "/servers"),
			DisableDiskCheck: config.System.Sftp.DisableDiskChecking,
		},
	}

	return c.Initalize()
}
