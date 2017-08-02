package api

func (api *API) registerServerRoutes() {

	api.router.GET("/servers", AuthHandler("c:list"), handleGetServers)

	api.router.POST("/servers", AuthHandler("c:create"), handlePostServers)
	api.router.DELETE("/server/:server", AuthHandler("g:server:delete"), handleDeleteServers)
	api.router.GET("/server/:server", AuthHandler("s:get"), handleGetServer)
	api.router.PATCH("/server/:server", AuthHandler("s:config"), handlePatchServer)

	api.router.POST("/server/:server/reinstall", AuthHandler("s:install-server"), handlePostServerReinstall)
	api.router.POST("/server/:server/rebuild", AuthHandler("g:server:rebuild"), handlePostServerRebuild)
	api.router.POST("/server/:server/password", AuthHandler(""), handlePostServerPassword)
	api.router.POST("/server/:server/power", AuthHandler("s:power"), handlePutServerPower)
	api.router.POST("/server/:server/command", AuthHandler("s:command"), handlePostServerCommand)
	api.router.GET("/server/:server/log", AuthHandler("s:console"), handleGetServerLog)
	api.router.POST("/server/:server/suspend", AuthHandler(""), handlePostServerSuspend)
	api.router.POST("/server/:server/unsuspend", AuthHandler(""), handlePostServerUnsuspend)
}
