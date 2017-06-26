package tools

import (
	"time"

	rotatelogs "github.com/lestrrat/go-file-rotatelogs"
	"github.com/rifflock/lfshook"
	log "github.com/Sirupsen/logrus"
)

func ConfigureLogging() {

	log.SetFormatter(&log.TextFormatter{
		DisableTimestamp: true,
	})

	path := "logs/"
	writer := rotatelogs.New(
		path+"pterodactyld.%Y%m%d-%H%M.log",
		rotatelogs.WithLinkName(path),
		rotatelogs.WithMaxAge(time.Duration(86400)*time.Second),
		rotatelogs.WithRotationTime(time.Duration(604800)*time.Second),
	)

	log.AddHook(lfshook.NewHook(lfshook.WriterMap{
		log.InfoLevel:  writer,
		log.ErrorLevel: writer,
		log.FatalLevel: writer,
		log.PanicLevel: writer,
	}))
}
