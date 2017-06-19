package config

import (
	"github.com/spf13/viper"
)

// Config contains the configuration of the Pterodactyl Daemon
type Config struct {

	// Web contains the settings of the api webserver
	Web struct {
		// ListenAddress is the address to bind the api webserver to
		ListenAddress string `json:"address"`
		// ListenPort is the port to bind the api webserver to
		ListenPort int16 `json:"port"`

		// SSL contains https configuration for the api webserver
		SSL struct {
			// Enabled allows to enable or disable ssl
			Enabled bool `json:"enabled"`
			// Certificate is the certificate file path to use
			Certificate string `json:"certificate"`
			// Key is the path to the private key for the certificate
			Key string `json:"key"`
		} `json:"ssl"`

		// Uploads contains file upload configuration
		Uploads struct {
			// MaximumSize is the maximum file upload size
			MaximumSize int64 `json:"maximumSize"`
		} `json:"uploads"`
	} `json:"web"`

	// Docker contains docker related configuration
	Docker struct {
		// Socket is the path to the docker control socket
		Socket string `json:"socket"`
		// AutoupdateImages allows to disable automatic Image updates
		AutoupdateImages bool `json:"autoupdateImages"`
		// NetworkInterface is the interface for the pterodactyl network
		NetworkInterface string `json:"networkInterface"`
		// TimezonePath is the path to the timezone file to mount in the containers
		TimezonePath string `json:"timezonePath"`
	} `json:"docker"`

	// Sftp contains information on the integrated sftp server
	Sftp struct {
		// Path is the base path of the sftp server
		Path string `json:"path"`
		// Port is the port to bind the sftp server to
		Port int16 `json:"port"`
	} `json:"sftp"`

	// Query contains parameters related to queriying of running gameservers
	Query struct {
		KillOnFail bool `json:"killOnFail"`
		FailLimit  bool `json:"failLimit"`
	} `json:"query"`

	// Remote is the url of the panel
	Remote string `json:"remote"`

	// Log contains configuration related to logging
	Log struct {
		// Path is the folder where logfiles should be stored
		Path string `json:"path"`
		// Level is the preferred log level
		Level string `json:"level"`

		// DeleteAfterDays is the time in days after which logfiles are deleted
		// If set to <= 0 logs are kept forever
		DeleteAfterDays int `json:"deleteAfterDays"`
	} `json:"log"`
}

// LoadConfiguration loads the configuration from the disk.
func LoadConfiguration() error {
	viper.SetConfigName("config")
	viper.AddConfigPath(".")
	// Find and read the config file
	if err := viper.ReadInConfig(); err != nil {
		return err
	}

	return nil
}

func setDefaults() {

}
