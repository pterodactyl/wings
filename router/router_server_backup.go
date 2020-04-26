package router

import (
	"errors"
	"fmt"
	"github.com/gin-gonic/gin"
	"github.com/pterodactyl/wings/server"
	"github.com/pterodactyl/wings/server/backup"
	"go.uber.org/zap"
	"net/http"
)

// Backs up a server.
func postServerBackup(c *gin.Context) {
	s := GetServer(c.Param("server"))

	data := &backup.Request{}
	c.BindJSON(&data)

	switch data.Adapter {
	case backup.LocalBackupAdapter:
		adapter, err := data.NewLocalBackup()
		if err != nil {
			TrackedServerError(err, s).AbortWithServerError(c)
			return
		}

		go func(b *backup.LocalBackup, serv *server.Server) {
			if err := serv.BackupLocal(b); err != nil {
				zap.S().Errorw("failed to generate backup for server", zap.Error(err))
			}
		}(adapter, s)
	case backup.S3BackupAdapter:
		TrackedServerError(errors.New(fmt.Sprintf("unsupported backup adapter [%s] provided", data.Adapter)), s).AbortWithServerError(c)
	default:
		TrackedServerError(errors.New(fmt.Sprintf("unknown backup adapter [%s] provided", data.Adapter)), s).AbortWithServerError(c)
	}

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
