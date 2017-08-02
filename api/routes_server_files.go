package api

func (api *API) registerServerFileRoutes() {
	// TODO: better and more consistent route names.

	api.router.POST("/server/file/folder", AuthHandler("s:files:create"), handlePostFilesFolder)

	api.router.GET("/server/directory/:directory", AuthHandler("s:files:get"), handleGetDirectory)

	api.router.POST("/server/file/copy", AuthHandler("s:files:copy"), handlePostFileCopy)

	api.router.POST("/server/file/{move,rename}", AuthHandler("s:files:move"), handlePostFileMove)

	api.router.POST("/server/file/delete", AuthHandler("s:files:delete"), handlePostFileDelete)

	api.router.POST("/server/file/compress", AuthHandler("s:files:compress"), handlePostFileCompress)

	api.router.POST("/server/file/decompress", AuthHandler("s:files:decompress"), handlePostFileDecompress)

	api.router.GET("/server/file/stat/:file", AuthHandler("s:files:"), handleGetFileStat)

	api.router.GET("/server/file/f/:file", AuthHandler("s:files:read"), handleGetFile)

	api.router.POST("/server/file/save", AuthHandler("s:files:post"), handlePostFile)

	api.router.DELETE("/server/file/f/:file", AuthHandler("s:files:delete"), handleDeleteFile)

	api.router.GET("/server/file/download/:token", handleGetDownloadFile)
}
