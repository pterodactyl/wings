package config

import (
	"github.com/apex/log"
	"os"
	"path"
)

// Defines basic system configuration settings.
type SystemConfiguration struct {
	// The root directory where all of the pterodactyl data is stored at.
	RootDirectory string `default:"/var/lib/pterodactyl" yaml:"root_directory"`

	// Directory where logs for server installations and other wings events are logged.
	LogDirectory string `default:"/var/log/pterodactyl" yaml:"log_directory"`

	// Directory where the server data is stored at.
	Data string `default:"/var/lib/pterodactyl/volumes" yaml:"data"`

	// Directory where server archives for transferring will be stored.
	ArchiveDirectory string `default:"/var/lib/pterodactyl/archives" yaml:"archive_directory"`

	// Directory where local backups will be stored on the machine.
	BackupDirectory string `default:"/var/lib/pterodactyl/backups" yaml:"backup_directory"`

	// The user that should own all of the server files, and be used for containers.
	Username string `default:"pterodactyl" yaml:"username"`

	// Definitions for the user that gets created to ensure that we can quickly access
	// this information without constantly having to do a system lookup.
	User struct {
		Uid int
		Gid int
	}

	// Determines if Wings should detect a server that stops with a normal exit code of
	// "0" as being crashed if the process stopped without any Wings interaction. E.g.
	// the user did not press the stop button, but the process stopped cleanly.
	DetectCleanExitAsCrash bool `default:"true" yaml:"detect_clean_exit_as_crash"`

	Sftp SftpConfiguration `yaml:"sftp"`
}

// Ensures that all of the system directories exist on the system. These directories are
// created so that only the owner can read the data, and no other users.
func (sc *SystemConfiguration) ConfigureDirectories() error {
	log.WithField("path", sc.RootDirectory).Debug("ensuring root data directory exists")
	if err := os.MkdirAll(sc.RootDirectory, 0700); err != nil {
		return err
	}

	log.WithField("path", sc.Data).Debug("ensuring server data directory exists")
	if err := os.MkdirAll(sc.Data, 0700); err != nil {
		return err
	}

	log.WithField("path", sc.ArchiveDirectory).Debug("ensuring archive data directory exists")
	if err := os.MkdirAll(sc.ArchiveDirectory, 0700); err != nil {
		return err
	}

	log.WithField("path", sc.BackupDirectory).Debug("ensuring backup data directory exists")
	if err := os.MkdirAll(sc.BackupDirectory, 0700); err != nil {
		return err
	}

	return nil
}

// Returns the location of the JSON file that tracks server states.
func (sc *SystemConfiguration) GetStatesPath() string {
	return path.Join(sc.RootDirectory, "states.json")
}

// Returns the location of the JSON file that tracks server states.
func (sc *SystemConfiguration) GetInstallLogPath() string {
	return path.Join(sc.LogDirectory, "install/")
}
