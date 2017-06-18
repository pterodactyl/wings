package config

import (
	"github.com/spf13/viper"
)

// Config contains the configuration of the Pterodactyl Daemon
type Config struct {

	// Log contains configuration related to logging
	Log struct {

		// DeleteAfterDays is the time in days after which logfiles are deleted
		// If set to <= 0 logs are kept forever
		DeleteAfterDays int
	} `json:"log"`
}

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
