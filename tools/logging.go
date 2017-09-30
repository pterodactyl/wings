package tools

import (
	"time"

	"github.com/lestrrat/go-file-rotatelogs"
	"github.com/rifflock/lfshook"
	log "github.com/sirupsen/logrus"
	"github.com/spf13/viper"

	"github.com/Pterodactyl/wings/config"
)

func InitLogging() {
	log.SetFormatter(&log.TextFormatter{
		DisableTimestamp: true,
	})

	log.SetLevel(log.InfoLevel)
}

// ConfigureLogging configures logrus to our needs
func ConfigureLogging() error {

	path := viper.GetString(config.LogPath)
	writer := rotatelogs.New(
		path+"wings.%Y%m%d-%H%M.log",
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
