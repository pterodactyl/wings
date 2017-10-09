package api

func (api *InternalAPI) RegisterRoutes() {
	// Register routes for v1 of the API. This API should be fully backwards compatable with
	// the existing Nodejs Daemon API.
	v1 := api.router.Group("/v1")
	{
		v1.GET("", AuthHandler(""), GetIndex)
		v1.PATCH("/config", AuthHandler("c:config"), PatchConfiguration)

		v1.GET("/servers", AuthHandler("c:list"), handleGetServers)
		v1.POST("/servers", AuthHandler("c:create"), handlePostServers)

		v1ServerRoutes := v1.Group("/servers/:server")
		{
			v1ServerRoutes.GET("", AuthHandler("s:get"), handleGetServer)
			v1ServerRoutes.PATCH("", AuthHandler("s:config"), handlePatchServer)
			v1ServerRoutes.DELETE("", AuthHandler("g:server:delete"), handleDeleteServer)
			v1ServerRoutes.POST("/reinstall", AuthHandler("s:install-server"), handlePostServerReinstall)
			v1ServerRoutes.POST("/rebuild", AuthHandler("g:server:rebuild"), handlePostServerRebuild)
			v1ServerRoutes.POST("/password", AuthHandler(""), handlePostServerPassword)
			v1ServerRoutes.POST("/power", AuthHandler("s:power"), handlePostServerPower)
			v1ServerRoutes.POST("/command", AuthHandler("s:command"), handlePostServerCommand)
			v1ServerRoutes.GET("/log", AuthHandler("s:console"), handleGetServerLog)
			v1ServerRoutes.POST("/suspend", AuthHandler(""), handlePostServerSuspend)
			v1ServerRoutes.POST("/unsuspend", AuthHandler(""), handlePostServerUnsuspend)
		}

		//v1ServerFileRoutes := v1.Group("/servers/:server/files")
		//{
		//	v1ServerFileRoutes.GET("/file/:file", AuthHandler("s:files:read"), handleGetFile)
		//	v1ServerFileRoutes.GET("/stat/:file", AuthHandler("s:files:"), handleGetFileStat)
		//	v1ServerFileRoutes.GET("/dir/:directory", AuthHandler("s:files:get"), handleGetDirectory)
		//
		//	v1ServerFileRoutes.POST("/dir/:directory", AuthHandler("s:files:create"), handlePostFilesFolder)
		//	v1ServerFileRoutes.POST("/file/:file", AuthHandler("s:files:post"), handlePostFile)
		//
		//	v1ServerFileRoutes.POST("/copy/:file", AuthHandler("s:files:copy"), handlePostFileCopy)
		//	v1ServerFileRoutes.POST("/move/:file", AuthHandler("s:files:move"), handlePostFileMove)
		//	v1ServerFileRoutes.POST("/rename/:file", AuthHandler("s:files:move"), handlePostFileMove)
		//	v1ServerFileRoutes.POST("/compress/:file", AuthHandler("s:files:compress"), handlePostFileCompress)
		//	v1ServerFileRoutes.POST("/decompress/:file", AuthHandler("s:files:decompress"), handlePostFileDecompress)
		//
		//	v1ServerFileRoutes.DELETE("/file/:file", AuthHandler("s:files:delete"), handleDeleteFile)
		//
		//	v1ServerFileRoutes.GET("/download/:token", handleGetDownloadFile)
		//}
	}
}
