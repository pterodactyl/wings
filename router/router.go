package router

import (
"emperror.dev/errors"
"github.com/apex/log"
"github.com/gin-gonic/gin"

"github.com/pterodactyl/wings/config"
"github.com/pterodactyl/wings/remote"
"github.com/pterodactyl/wings/router/middleware"
wserver "github.com/pterodactyl/wings/server"

restic "github.com/pterodactyl/wings/internal/api/restic"
	"regexp"

	"emperror.dev/errors"
	"github.com/apex/log"
	"github.com/gin-gonic/gin"

	"github.com/pterodactyl/wings/config"
	"github.com/pterodactyl/wings/remote"
	"github.com/pterodactyl/wings/router/middleware"
	wserver "github.com/pterodactyl/wings/server"
)

var tokenRegex = regexp.MustCompile(`([?|&]token=)([^&]+)($|&)`)

// Configure configures the routing infrastructure for this daemon instance.
func Configure(m *wserver.Manager, client remote.Client) *gin.Engine {
	gin.SetMode("release")

	router := gin.New()
	router.Use(gin.Recovery())
	if err := router.SetTrustedProxies(config.Get().Api.TrustedProxies); err != nil {
		panic(errors.WithStack(err))
		return nil
	}
	router.Use(middleware.AttachRequestID(), middleware.CaptureErrors(), middleware.SetAccessControlHeaders())
	router.Use(middleware.AttachServerManager(m), middleware.AttachApiClient(client))
	// @todo log this into a different file so you can setup IP blocking for abusive requests and such.
	// This should still dump requests in debug mode since it does help with understanding the request
	// lifecycle and quickly seeing what was called leading to the logs. However, it isn't feasible to mix
	// this output in production and still get meaningful logs from it since they'll likely just be a huge
	// spamfest.
	router.Use(gin.LoggerWithFormatter(func(params gin.LogFormatterParams) string {
		log.WithFields(log.Fields{
			"client_ip":  params.ClientIP,
			"status":     params.StatusCode,
			"latency":    params.Latency,
			"request_id": params.Keys["request_id"],
		}).Debugf("%s %s", params.Method, tokenRegex.ReplaceAllString(params.Path, "$1***$3"))

		return ""
	}))

	// These routes use signed URLs to validate access to the resource being requested.
	router.GET("/download/backup", getDownloadBackup)
	router.GET("/download/restic-backup", getDownloadResticBackup)
	router.GET("/download/file", getDownloadFile)
	router.POST("/upload/file", postServerUploadFiles)

	// This route is special it sits above all the other requests because we are
	// using a JWT to authorize access to it, therefore it needs to be publicly
	// accessible.
	router.GET("/api/servers/:server/ws", middleware.ServerExists(), getServerWebsocket)

	// This request is called by another daemon when a server is going to be transferred out.
	// This request does not need the AuthorizationMiddleware as the panel should never call it
	// and requests are authenticated through a JWT the panel issues to the other daemon.
	router.POST("/api/transfers", postTransfers)

	// All the routes beyond this mount will use an authorization middleware
	// and will not be accessible without the correct Authorization header provided.
	protected := router.Use(middleware.RequireAuthorization())
	protected.POST("/api/update", postUpdateConfiguration)
	protected.GET("/api/system", getSystemInformation)
	protected.GET("/api/servers", getAllServers)
	protected.POST("/api/servers", postCreateServer)
	protected.DELETE("/api/transfers/:server", deleteTransfer)
	protected.POST("/api/deauthorize-user", postDeauthorizeUser)
	// Restic archived repo browser endpoints (admin tooling via panel).
	protected.GET("/api/restic/archive", restic.ListArchivedRepos)
	protected.GET("/api/restic/archive/:archiveId/download", restic.DownloadArchivedRepo)
	protected.DELETE("/api/restic/archive/:archiveId", restic.DeleteArchivedRepo)

	// These are server specific routes, and require that the request be authorized, and
	// that the server exist on the Daemon.
	server := router.Group("/api/servers/:server")
	server.Use(middleware.RequireAuthorization(), middleware.ServerExists())
	{
		server.GET("", getServer)
		server.DELETE("", deleteServer)

		server.GET("/logs", getServerLogs)
		server.POST("/power", postServerPower)
		server.POST("/commands", postServerCommands)
		server.POST("/install", postServerInstall)
		server.POST("/reinstall", postServerReinstall)
		server.POST("/sync", postServerSync)
		server.POST("/ws/deny", postServerDenyWSTokens)

		// This archive request causes the archive to start being created
		// this should only be triggered by the panel.
		server.POST("/transfer", postServerTransfer)
		server.DELETE("/transfer", deleteServerTransfer)

			// Restic backup endpoints
			server.POST("/backups/restic", restic.CreateServerResticBackup)
			server.GET("/backups/restic", restic.ListServerResticBackups)
			server.GET("/backups/restic/status", restic.GetServerResticBackupStatus)
			server.GET("/backups/restic/stats", restic.GetServerResticStats)
			server.GET("/backups/restic/restore/status", restic.GetServerResticRestoreStatus)
			server.POST("/backups/restic/prune", restic.PruneServerResticBackup)
			server.GET("/backups/restic/prune/status", restic.GetServerResticPruneStatus)
			server.POST("/backups/restic/:backupId/lock", restic.LockServerResticBackup)
			server.POST("/backups/restic/:backupId/unlock", restic.UnlockServerResticBackup)
			server.DELETE("/backups/restic/:backupId", restic.DeleteServerResticBackup)
			server.GET("/backups/restic/locks", restic.GetServerResticLocks)
			server.POST("/backups/restic/unlock", restic.UnlockServerResticRepo)
			server.DELETE("/backups/restic/repo", restic.DeleteServerResticRepo)
			server.POST("/backups/restic/repo/delete", restic.DeleteServerResticRepo)
			server.GET("/backups/restic/repo/exists", restic.CheckServerResticRepo)
			server.GET("/backups/restic/repo/size", restic.GetServerResticRepoDiskUsage)
			server.POST("/backups/restic/repo/check", restic.CheckServerResticRepoHealth)
			server.GET("/backups/restic/repo/check/status", restic.GetServerResticRepoHealthStatus)
			server.POST("/backups/restic/:backupId/prepare", restic.PrepareServerResticBackupHandler)
			server.GET("/backups/restic/:backupId/prepare/status", restic.GetServerResticBackupPrepareStatus)
			server.POST("/backups/restic/:backupId/restore", restic.RestoreServerResticBackupHandler)
			// Backwards-compatible restore route
			server.POST("/backups/restic/restore/:backupId", restic.RestoreServerResticBackupHandler)
			server.GET("/backups/restic/:backupId/download", restic.DownloadServerResticBackup)

		files := server.Group("/files")
		{
			files.GET("/contents", getServerFileContents)
			files.GET("/list-directory", getServerListDirectory)
			files.PUT("/rename", putServerRenameFiles)
			files.POST("/copy", postServerCopyFile)
			files.POST("/write", postServerWriteFile)
			files.POST("/create-directory", postServerCreateDirectory)
			files.POST("/delete", postServerDeleteFiles)
			files.POST("/compress", postServerCompressFiles)
			files.POST("/decompress", postServerDecompressFiles)
			files.POST("/chmod", postServerChmodFile)

			files.GET("/pull", middleware.RemoteDownloadEnabled(), getServerPullingFiles)
			files.POST("/pull", middleware.RemoteDownloadEnabled(), postServerPullRemoteFile)
			files.DELETE("/pull/:download", middleware.RemoteDownloadEnabled(), deleteServerPullRemoteFile)
		}

		backup := server.Group("/backup")
		{
			backup.POST("", postServerBackup)
			backup.POST(":backup/restore", postServerRestoreBackup)
			backup.DELETE(":backup", deleteServerBackup)
		}
	}

	return router
}
