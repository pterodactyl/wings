package router

import (
	"github.com/gin-gonic/gin"
	"github.com/pterodactyl/wings/server"
	"go.uber.org/zap"
	"net/http"
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
