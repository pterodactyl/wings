package tools

import (
	"time"

	rotatelogs "github.com/lestrrat/go-file-rotatelogs"
	"github.com/rifflock/lfshook"
	log "github.com/sirupsen/logrus"

	"github.com/schrej/wings/config"
)

// ConfigureLogging configures logrus to our needs
func ConfigureLogging() error {

	log.SetFormatter(&log.TextFormatter{
		DisableTimestamp: true,
	})

	path := config.Get().Log.Path
	writer := rotatelogs.New(
		path+"wings.%Y%m%d-%H%M.log",
		rotatelogs.WithLinkName(path),
		rotatelogs.WithMaxAge(time.Duration(config.Get().Log.DeleteAfterDays)*time.Hour*24),
		rotatelogs.WithRotationTime(time.Duration(604800)*time.Second),
	)

	level := config.Get().Log.Level

	// In debug mode the log level is always debug
	if config.Get().Debug {
		level = "debug"
	}

	log.SetLevel(log.DebugLevel)

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

	log.AddHook(lfshook.NewHook(lfshook.WriterMap{
		log.DebugLevel: writer,
		log.InfoLevel:  writer,
		log.WarnLevel:  writer,
		log.ErrorLevel: writer,
		log.FatalLevel: writer,
	}))

	log.Info("Log level: " + level)
	log.Debug("Test")

	return nil
}
