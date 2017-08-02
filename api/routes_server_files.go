package api

func (api *API) registerServerFileRoutes() {
	api.router.GET("/server/:server/files/file/:file", AuthHandler("s:files:read"), handleGetFile)
	api.router.GET("/server/:server/files/stat/:file", AuthHandler("s:files:"), handleGetFileStat)
	api.router.GET("/server/:server/files/dir/:directory", AuthHandler("s:files:get"), handleGetDirectory)

	api.router.POST("/server/:server/files/dir/:directory", AuthHandler("s:files:create"), handlePostFilesFolder)
	api.router.POST("/server/:server/files/file/:file", AuthHandler("s:files:post"), handlePostFile)

	api.router.POST("/server/:server/files/copy/:file", AuthHandler("s:files:copy"), handlePostFileCopy)
	api.router.POST("/server/:server/files/move/:file", AuthHandler("s:files:move"), handlePostFileMove)
	api.router.POST("/server/:server/files/rename/:file", AuthHandler("s:files:move"), handlePostFileMove)
	api.router.POST("/server/:server/files/compress/:file", AuthHandler("s:files:compress"), handlePostFileCompress)
	api.router.POST("/server/:server/files/decompress/:file", AuthHandler("s:files:decompress"), handlePostFileDecompress)

	api.router.DELETE("/server/:server/files/file/:file", AuthHandler("s:files:delete"), handleDeleteFile)

	api.router.GET("/server/:server/files/download/:token", handleGetDownloadFile)
}
