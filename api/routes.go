package api

func (api *InternalAPI) RegisterRoutes() {
	// Register routes for v1 of the API. This API should be fully backwards compatable with
	// the existing Nodejs Daemon API.
	v1 := api.router.Group("/v1")
	{
		v1.GET("/", AuthHandler(""), GetIndex)
		v1.PATCH("/config", AuthHandler("c:config"), PatchConfiguration)

		v1BaseRoutes := v1.Group("/server")
		{
			v1BaseRoutes.GET("/", AuthHandler("c:list"), ListServers)
			v1BaseRoutes.POST("/", AuthHandler("c:create"), StoreServer)
		}

		v1ServerRoutes := v1.Group("/server/:server")
		{
			v1ServerRoutes.GET("/", AuthHandler("s:get"), ViewServer)
			v1ServerRoutes.GET("/log", AuthHandler("s:console"), GetLogForServer)

			v1ServerRoutes.POST("/reinstall", AuthHandler("s:install-server"), ReinstallServer)
			v1ServerRoutes.POST("/rebuild", AuthHandler("g:server:rebuild"), RebuildServer)
			v1ServerRoutes.POST("/password", AuthHandler(""), SetServerPassword)
			v1ServerRoutes.POST("/power", AuthHandler("s:power"), PowerServer)
			v1ServerRoutes.POST("/command", AuthHandler("s:command"), SendCommandToServer)
			v1ServerRoutes.POST("/suspend", AuthHandler(""), SuspendServer)
			v1ServerRoutes.POST("/unsuspend", AuthHandler(""), UnsuspendServer)

			v1ServerRoutes.PATCH("/", AuthHandler("s:config"), UpdateServer)
			v1ServerRoutes.DELETE("/", AuthHandler("g:server:delete"), DeleteServer)
		}

		v1ServerFileRoutes := v1.Group("/server/:server/files")
		{
			v1ServerFileRoutes.GET("/file/:file", AuthHandler("s:files:read"), ReadFileContents)
			v1ServerFileRoutes.GET("/stat/:file", AuthHandler("s:files:get"), StatFile)
			v1ServerFileRoutes.GET("/dir/:directory", AuthHandler("s:files:get"), ListDirectory)
			v1ServerFileRoutes.GET("/download/:token", DownloadFile)

			v1ServerFileRoutes.POST("/dir/:directory", AuthHandler("s:files:create"), StoreDirectory)
			v1ServerFileRoutes.POST("/file/:file", AuthHandler("s:files:post"), WriteFileContents)
			v1ServerFileRoutes.POST("/copy/:file", AuthHandler("s:files:copy"), CopyFile)
			v1ServerFileRoutes.POST("/move/:file", AuthHandler("s:files:move"), MoveFile)
			v1ServerFileRoutes.POST("/rename/:file", AuthHandler("s:files:move"), MoveFile)
			v1ServerFileRoutes.POST("/compress/:file", AuthHandler("s:files:compress"), CompressFile)
			v1ServerFileRoutes.POST("/decompress/:file", AuthHandler("s:files:decompress"), DecompressFile)

			v1ServerFileRoutes.DELETE("/file/:file", AuthHandler("s:files:delete"), DeleteFile)
		}
	}
}
