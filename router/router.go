package router

import (
	"github.com/gin-gonic/gin"
)

// Configures the routing infrastructure for this daemon instance.
func Configure() *gin.Engine {
	router := gin.Default()
	router.Use(SetAccessControlHeaders)

	router.OPTIONS("/api/system", func(c *gin.Context) {
		c.Status(200)
	})

	// These routes use signed URLs to validate access to the resource being requested.
	router.GET("/download/backup", getDownloadBackup)
	router.GET("/download/file", getDownloadFile)

	// This route is special it sits above all of the other requests because we are
	// using a JWT to authorize access to it, therefore it needs to be publicly
	// accessible.
	router.GET("/api/servers/:server/ws", getServerWebsocket)

	// This request is called by another daemon when a server is going to be transferred out.
	// This request does not need the AuthorizationMiddleware as the panel should never call it
	// and requests are authenticated through a JWT the panel issues to the other daemon.
	router.GET("/api/servers/:server/archive", getServerArchive)

	// All of the routes beyond this mount will use an authorization middleware
	// and will not be accessible without the correct Authorization header provided.
	protected := router.Use(AuthorizationMiddleware)
	protected.GET("/api/system", getSystemInformation)
	protected.GET("/api/servers", getAllServers)
	protected.POST("/api/servers", postCreateServer)
	protected.POST("/api/transfer", postTransfer)

	// These are server specific routes, and require that the request be authorized, and
	// that the server exist on the Daemon.
	server := router.Group("/api/servers/:server")
	server.Use(AuthorizationMiddleware, ServerExists)
	{
		server.GET("", getServer)
		server.PATCH("", patchServer)
		server.DELETE("", deleteServer)

		server.GET("/logs", getServerLogs)
		server.POST("/power", postServerPower)
		server.POST("/commands", postServerCommands)
		server.POST("/install", postServerInstall)
		server.POST("/reinstall", postServerReinstall)
		server.POST("/backup", postServerBackup)

		// This archive request causes the archive to start being created
		// this should only be triggered by the panel.
		server.POST("/archive", postServerArchive)

		files := server.Group("/files")
		{
			files.GET("/contents", getServerFileContents)
			files.GET("/list-directory", getServerListDirectory)
			files.PUT("/rename", putServerRenameFile)
			files.POST("/copy", postServerCopyFile)
			files.POST("/write", postServerWriteFile)
			files.POST("/create-directory", postServerCreateDirectory)
			files.POST("/delete", postServerDeleteFile)
		}
	}

	return router
}
