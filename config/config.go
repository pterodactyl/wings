package config

import (
	"github.com/spf13/viper"
)

// Config contains the configuration of the Pterodactyl Daemon
type Config struct {
	// Debug enables debug mode
	Debug bool `mapstructure:"debug"`

	// Web contains the settings of the api webserver
	Web struct {
		// ListenHost is the host address to bind the api webserver to
		ListenHost string `mapstructure:"host"`
		// ListenPort is the port to bind the api webserver to
		ListenPort int16 `mapstructure:"port"`

		// SSL contains https configuration for the api webserver
		SSL struct {
			// Enabled allows to enable or disable ssl
			Enabled bool `mapstructure:"enabled"`
			// GenerateLetsEncrypt
			GenerateLetsEncrypt bool `mapstructure:"GenerateLetsEncrypt"`
			// Certificate is the certificate file path to use
			Certificate string `mapstructure:"certificate"`
			// Key is the path to the private key for the certificate
			Key string `mapstructure:"key"`
		} `mapstructure:"ssl"`

		// Uploads contains file upload configuration
		Uploads struct {
			// MaximumSize is the maximum file upload size
			MaximumSize int64 `mapstructure:"maximumSize"`
		} `mapstructure:"uploads"`
	} `mapstructure:"web"`

	// Docker contains docker related configuration
	Docker struct {
		// Socket is the path to the docker control socket
		Socket string `mapstructure:"socket"`
		// AutoupdateImages allows to disable automatic Image updates
		AutoupdateImages bool `mapstructure:"autoupdateImages"`
		// NetworkInterface is the interface for the pterodactyl network
		NetworkInterface string `mapstructure:"networkInterface"`
		// TimezonePath is the path to the timezone file to mount in the containers
		TimezonePath string `mapstructure:"timezonePath"`
	} `mapstructure:"docker"`

	// Sftp contains information on the integrated sftp server
	Sftp struct {
		// Path is the base path of the sftp server
		Path string `mapstructure:"path"`
		// Port is the port to bind the sftp server to
		Port int16 `mapstructure:"port"`
	} `mapstructure:"sftp"`

	// Query contains parameters related to queriying of running gameservers
	Query struct {
		KillOnFail bool `mapstructure:"killOnFail"`
		FailLimit  bool `mapstructure:"failLimit"`
	} `mapstructure:"query"`

	// Remote is the url of the panel
	Remote string `mapstructure:"remote"`

	// Log contains configuration related to logging
	Log struct {
		// Path is the folder where logfiles should be stored
		Path string `mapstructure:"path"`
		// Level is the preferred log level
		// It is overriden to debug when debug mode is enabled
		Level string `mapstructure:"level"`

		// DeleteAfterDays is the time in days after which logfiles are deleted
		// If set to <= 0 logs are kept forever
		DeleteAfterDays int `mapstructure:"deleteAfterDays"`
	} `mapstructure:"log"`
}

var config *Config

func LoadConfiguration(path *string) error {
	if path != nil {
		viper.SetConfigFile(*path)
	} else {
		viper.AddConfigPath("./")
		viper.SetConfigName("config")
	}

	// Find and read the config file
	if err := viper.ReadInConfig(); err != nil {
		return err
	}

	config = new(Config)
	if err := viper.Unmarshal(config); err != nil {
		return err
	}

	return nil
}

// Get returns the configuration
func Get() *Config {
	return config
}

func setDefaults() {

}
