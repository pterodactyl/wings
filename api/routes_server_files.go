package api

func (api *API) registerServerFileRoutes() {
	api.router.GET("/servers/:server/files/file/:file", AuthHandler("s:files:read"), handleGetFile)
	api.router.GET("/servers/:server/files/stat/:file", AuthHandler("s:files:"), handleGetFileStat)
	api.router.GET("/servers/:server/files/dir/:directory", AuthHandler("s:files:get"), handleGetDirectory)

	api.router.POST("/servers/:server/files/dir/:directory", AuthHandler("s:files:create"), handlePostFilesFolder)
	api.router.POST("/servers/:server/files/file/:file", AuthHandler("s:files:post"), handlePostFile)

	api.router.POST("/servers/:server/files/copy/:file", AuthHandler("s:files:copy"), handlePostFileCopy)
	api.router.POST("/servers/:server/files/move/:file", AuthHandler("s:files:move"), handlePostFileMove)
	api.router.POST("/servers/:server/files/rename/:file", AuthHandler("s:files:move"), handlePostFileMove)
	api.router.POST("/servers/:server/files/compress/:file", AuthHandler("s:files:compress"), handlePostFileCompress)
	api.router.POST("/servers/:server/files/decompress/:file", AuthHandler("s:files:decompress"), handlePostFileDecompress)

	api.router.DELETE("/servers/:server/files/file/:file", AuthHandler("s:files:delete"), handleDeleteFile)

	api.router.GET("/servers/:server/files/download/:token", handleGetDownloadFile)
}
