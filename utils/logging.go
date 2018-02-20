package utils

import (
	"os"
	"path/filepath"
	"time"

	"github.com/pterodactyl/wings/constants"

	rotatelogs "github.com/lestrrat/go-file-rotatelogs"
	"github.com/rifflock/lfshook"
	log "github.com/sirupsen/logrus"
	"github.com/spf13/viper"

	"github.com/pterodactyl/wings/config"
)

// InitLogging initalizes the logging library for first use.
func InitLogging() {
	log.SetFormatter(&log.TextFormatter{
		DisableTimestamp: true,
	})

	log.SetLevel(log.InfoLevel)
}

// ConfigureLogging applies the configuration to the logging library.
func ConfigureLogging() error {
	path := filepath.Clean(viper.GetString(config.LogPath))
	if err := os.MkdirAll(path, constants.DefaultFolderPerms); err != nil {
		return err
	}
	writer := rotatelogs.New(
		path+"/wings.%Y%m%d-%H%M.log",
		rotatelogs.WithLinkName(path),
		rotatelogs.WithMaxAge(time.Duration(viper.GetInt(config.LogDeleteAfterDays))*time.Hour*24),
		rotatelogs.WithRotationTime(time.Duration(604800)*time.Second),
	)

	log.AddHook(lfshook.NewHook(lfshook.WriterMap{
		log.DebugLevel: writer,
		log.InfoLevel:  writer,
		log.WarnLevel:  writer,
		log.ErrorLevel: writer,
		log.FatalLevel: writer,
	}))

	level := viper.GetString(config.LogLevel)

	// In debug mode the log level is always debug
	if viper.GetBool(config.Debug) {
		level = "debug"
	}

	// Apply log level
	switch level {
	case "debug":
		log.SetLevel(log.DebugLevel)

	case "info":
		log.SetLevel(log.InfoLevel)

	case "warn":
		log.SetLevel(log.WarnLevel)

	case "error":
		log.SetLevel(log.ErrorLevel)

	case "fatal":
		log.SetLevel(log.FatalLevel)

	case "panic":
		log.SetLevel(log.PanicLevel)
	}

	log.Info("Log level: " + level)

	return nil
}
