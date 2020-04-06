package router

import (
	"github.com/gin-gonic/gin"
	"github.com/pkg/errors"
	"github.com/pterodactyl/wings/api"
	"github.com/pterodactyl/wings/server"
	"go.uber.org/zap"
	"net/http"
	"time"
)

// Backs up a server.
func postServerBackup(c *gin.Context) {
	s := GetServer(c.Param("server"))

	var data struct{
		Uuid string `json:"uuid"`
		IgnoredFiles []string `json:"ignored_files"`
	}
	c.BindJSON(&data)

	go func(backup *server.Backup) {
		if err := backup.BackupAndNotify(); err != nil {
			zap.S().Errorw("failed to generate backup for server", zap.Error(err))
		}
	}(s.NewBackup(data.Uuid, data.IgnoredFiles))

	c.Status(http.StatusAccepted)
}

func getServerArchive(c *gin.Context) {
}

func postServerArchive(c *gin.Context) {
	s := GetServer(c.Param("server"))

	go func() {
		start := time.Now()

		if err := s.Archiver.Archive(); err != nil {
			zap.S().Errorw("failed to get archive for server", zap.String("server", s.Uuid), zap.Error(err))
			return
		}

		zap.S().Debugw("successfully created archive for server", zap.String("server", s.Uuid), zap.Duration("time", time.Now().Sub(start).Round(time.Microsecond)))

		r := api.NewRequester()
		rerr, err := r.SendArchiveStatus(s.Uuid, true)
		if rerr != nil || err != nil {
			if err != nil {
				zap.S().Errorw("failed to notify panel with archive status", zap.String("server", s.Uuid), zap.Error(err))
				return
			}

			zap.S().Errorw("panel returned an error when sending the archive status", zap.String("server", s.Uuid), zap.Error(errors.New(rerr.String())))
			return
		}

		zap.S().Debugw("successfully notified panel about archive status", zap.String("server", s.Uuid))
	}()

	c.Status(http.StatusAccepted)
}