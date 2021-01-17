package router

import (
	"net/http"
	"os"

	"emperror.dev/errors"
	"github.com/apex/log"
	"github.com/gin-gonic/gin"
	"github.com/pterodactyl/wings/router/middleware"
	"github.com/pterodactyl/wings/server"
	"github.com/pterodactyl/wings/server/backup"
)

// postServerBackup performs a backup against a given server instance using the
// provided backup adapter.
func postServerBackup(c *gin.Context) {
	s := middleware.ExtractServer(c)
	logger := middleware.ExtractLogger(c)
	var data backup.Request
	if err := c.BindJSON(&data); err != nil {
		return
	}

	adapter, err := data.AsBackup()
	if err != nil {
		middleware.CaptureAndAbort(c, err)
		return
	}
	// Attach the server ID and the request ID to the adapter log context for easier
	// parsing in the logs.
	adapter.WithLogContext(map[string]interface{}{
		"server":     s.Id(),
		"request_id": c.GetString("request_id"),
	})

	go func(b backup.BackupInterface, s *server.Server, logger *log.Entry) {
		if err := s.Backup(b); err != nil {
			logger.WithField("error", errors.WithStackIf(err)).Error("router: failed to generate server backup")
		}
	}(adapter, s, logger)

	c.Status(http.StatusAccepted)
}

// postServerRestoreBackup handles restoring a backup for a server by downloading
// or finding the given backup on the system and then unpacking the archive into
// the server's data directory. If the TruncateDirectory field is provided and
// is true all of the files will be deleted for the server.
//
// This endpoint will block until the backup is fully restored allowing for a
// spinner to be displayed in the Panel UI effectively.
func postServerRestoreBackup(c *gin.Context) {
	s := middleware.ExtractServer(c)
	logger := middleware.ExtractLogger(c)

	var data struct {
		UUID              string             `binding:"required,uuid" json:"uuid"`
		Adapter           backup.AdapterType `binding:"required,oneof=wings s3" json:"adapter"`
		TruncateDirectory bool               `json:"truncate_directory"`
		// A UUID is always required for this endpoint, however the download URL
		// is only present when the given adapter type is s3.
		DownloadUrl string `json:"download_url"`
	}
	if err := c.BindJSON(&data); err != nil {
		return
	}
	if data.Adapter == backup.S3BackupAdapter && data.DownloadUrl == "" {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "The download_url field is required when the backup adapter is set to S3."})
		return
	}

	logger.Info("processing server backup restore request")
	if data.TruncateDirectory {
		logger.Info(`recieved "truncate_directory" flag in request: deleting server files`)
		if err := s.Filesystem().TruncateRootDirectory(); err != nil {
			middleware.CaptureAndAbort(c, err)
			return
		}
	}

	// Now that we've cleaned up the data directory if necessary, grab the backup file
	// and attempt to restore it into the server directory.
	if data.Adapter == backup.LocalBackupAdapter {
		b, _, err := backup.LocateLocal(data.UUID)
		if err != nil {
			middleware.CaptureAndAbort(c, err)
			return
		}
		if err := b.Restore(s); err != nil {
			middleware.CaptureAndAbort(c, err)
			return
		}
	}
	c.Status(http.StatusNoContent)
}

// deleteServerBackup deletes a local backup of a server. If the backup is not
// found on the machine just return a 404 error. The service calling this
// endpoint can make its own decisions as to how it wants to handle that
// response.
func deleteServerBackup(c *gin.Context) {
	b, _, err := backup.LocateLocal(c.Param("backup"))
	if err != nil {
		// Just return from the function at this point if the backup was not located.
		if errors.Is(err, os.ErrNotExist) {
			c.AbortWithStatusJSON(http.StatusNotFound, gin.H{
				"error": "The requested backup was not found on this server.",
			})
			return
		}
		middleware.CaptureAndAbort(c, err)
		return
	}
	// I'm not entirely sure how likely this is to happen, however if we did manage to
	// locate the backup previously and it is now missing when we go to delete, just
	// treat it as having been successful, rather than returning a 404.
	if err := b.Remove(); err != nil && !errors.Is(err, os.ErrNotExist) {
		middleware.CaptureAndAbort(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}