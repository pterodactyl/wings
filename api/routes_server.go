package api

func (api *API) registerServerRoutes() {

	// Big Picture Actions
	api.router.GET("/servers", AuthHandler("c:list"), handleGetServers)

	api.router.POST("/servers", AuthHandler("c:create"), handlePostServers)

	api.router.DELETE("/servers", AuthHandler("g:server:delete"), handleDeleteServers)

	// Server Actions
	api.router.GET("/server", AuthHandler("s:get"), handleGetServer)

	api.router.PATCH("/server", AuthHandler("s:config"), handlePatchServer)

	api.router.PUT("/server", AuthHandler("s:config"), handlePutServer)

	api.router.POST("/server/reinstall", AuthHandler("s:install-server"), handlePostServerReinstall)

	api.router.POST("/server/password", AuthHandler(""), handlePostServerPassword)

	api.router.POST("/server/rebuild", AuthHandler("g:server:rebuild"), handlePostServerRebuild)

	api.router.PUT("/server/power", AuthHandler("s:power"), handlePutServerPower)

	api.router.POST("/server/command", AuthHandler("s:command"), handlePostServerCommand)

	api.router.GET("/server/log", AuthHandler("s:console"), handleGetServerLog)

	api.router.POST("/server/suspend", AuthHandler(""), handlePostServerSuspend)

	api.router.POST("/server/unsuspend", AuthHandler(""), handlePostServerUnsuspend)
}
