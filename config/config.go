package config

import (
	"github.com/spf13/viper"
)

// LoadConfiguration loads the configuration from a specified file
func LoadConfiguration(path string) error {
	if path != "" {
		viper.SetConfigFile(path)
	} else {
		viper.AddConfigPath("./")
		viper.SetConfigName("config")
	}

	// Find and read the config file
	if err := viper.ReadInConfig(); err != nil {
		return err
	}

	return nil
}

// StoreConfiguration stores the configuration to a specified file
func StoreConfiguration(path string) error {
	// TODO: Implement

	return nil
}

func setDefaults() {
	viper.SetDefault(Debug, false)
	viper.SetDefault(DataPath, "./data")
	viper.SetDefault(APIHost, "0.0.0.0")
	viper.SetDefault(APIPort, 8080)
	viper.SetDefault(SSLEnabled, false)
	viper.SetDefault(SSLGenerateLetsencrypt, false)
	viper.SetDefault(UploadsMaximumSize, 100000)
	viper.SetDefault(DockerSocket, "/var/run/docker.sock")
	viper.SetDefault(DockerAutoupdateImages, true)
	viper.SetDefault(DockerNetworkInterface, "127.18.0.0")
	viper.SetDefault(DockerTimezonePath, "/etc/timezone")
	viper.SetDefault(SftpHost, "0.0.0.0")
	viper.SetDefault(SftpPort, "2202")
	viper.SetDefault(LogPath, "./logs")
	viper.SetDefault(LogLevel, "info")
	viper.SetDefault(LogDeleteAfterDays, "30")
}

// ContainsAuthKey checks wether the config contains a specified authentication key
func ContainsAuthKey(key string) bool {
	for _, k := range viper.GetStringSlice(AuthKeys) {
		if k == key {
			return true
		}
	}
	return false
}
