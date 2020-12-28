package router

import (
	"emperror.dev/errors"
	"fmt"
	"github.com/gin-gonic/gin"
	"github.com/pterodactyl/wings/server"
	"github.com/pterodactyl/wings/server/backup"
	"net/http"
	"os"
)

// Backs up a server.
func postServerBackup(c *gin.Context) {
	s := GetServer(c.Param("server"))

	data := &backup.Request{}
	// BindJSON sends 400 if the request fails, all we need to do is return
	if err := c.BindJSON(&data); err != nil {
		return
	}

	var adapter backup.BackupInterface
	var err error

	switch data.Adapter {
	case backup.LocalBackupAdapter:
		adapter, err = data.NewLocalBackup()
	case backup.S3BackupAdapter:
		adapter, err = data.NewS3Backup()
	default:
		err = errors.New(fmt.Sprintf("unknown backup adapter [%s] provided", data.Adapter))
		return
	}

	if err != nil {
		NewServerError(err, s).Abort(c)
		return
	}

	// Attach the server ID to the backup log output for easier parsing.
	adapter.WithLogContext(map[string]interface{}{
		"server": s.Id(),
	})

	go func(b backup.BackupInterface, serv *server.Server) {
		if err := serv.Backup(b); err != nil {
			serv.Log().WithField("error", errors.WithStackIf(err)).Error("failed to generate backup for server")
		}
	}(adapter, s)

	c.Status(http.StatusAccepted)
}

// Deletes a local backup of a server. If the backup is not found on the machine just return
// a 404 error. The service calling this endpoint can make its own decisions as to how it wants
// to handle that response.
func deleteServerBackup(c *gin.Context) {
	s := GetServer(c.Param("server"))

	b, _, err := backup.LocateLocal(c.Param("backup"))
	if err != nil {
		// Just return from the function at this point if the backup was not located.
		if errors.Is(err, os.ErrNotExist) {
			c.AbortWithStatusJSON(http.StatusNotFound, gin.H{
				"error": "The requested backup was not found on this server.",
			})
			return
		}

		NewServerError(err, s).Abort(c)
		return
	}

	if err := b.Remove(); err != nil {
		// I'm not entirely sure how likely this is to happen, however if we did manage to locate
		// the backup previously and it is now missing when we go to delete, just treat it as having
		// been successful, rather than returning a 404.
		if !errors.Is(err, os.ErrNotExist) {
			NewServerError(err, s).Abort(c)
			return
		}
	}

	c.Status(http.StatusNoContent)
}
