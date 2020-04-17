package router

import (
	"github.com/gin-gonic/gin"
	"github.com/pterodactyl/wings/server"
	"github.com/pterodactyl/wings/server/backup"
	"go.uber.org/zap"
	"net/http"
)

// Backs up a server.
func postServerBackup(c *gin.Context) {
	s := GetServer(c.Param("server"))

	data := &backup.LocalBackup{}
	c.BindJSON(&data)

	go func(b *backup.LocalBackup, serv *server.Server) {
		if err := serv.BackupLocal(b); err != nil {
			zap.S().Errorw("failed to generate backup for server", zap.Error(err))
		}
	}(data, s)

	c.Status(http.StatusAccepted)
}

// Deletes a local backup of a server.
func deleteServerBackup(c *gin.Context) {
	s := GetServer(c.Param("server"))

	b, _, err := backup.LocateLocal(c.Param("backup"))
	if err != nil {
		TrackedServerError(err, s).AbortWithServerError(c)
		return
	}

	if err := b.Remove(); err != nil {
		TrackedServerError(err, s).AbortWithServerError(c)
		return
	}

	c.Status(http.StatusNoContent)
}