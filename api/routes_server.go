package api

func (api *API) registerServerRoutes() {

	api.router.GET("/servers", AuthHandler("c:list"), handleGetServers)

	api.router.POST("/servers", AuthHandler("c:create"), handlePostServers)
	api.router.GET("/servers/:server", AuthHandler("s:get"), handleGetServer)
	api.router.PATCH("/servers/:server", AuthHandler("s:config"), handlePatchServer)
	api.router.DELETE("/servers/:server", AuthHandler("g:server:delete"), handleDeleteServer)

	api.router.POST("/servers/:server/reinstall", AuthHandler("s:install-server"), handlePostServerReinstall)
	api.router.POST("/servers/:server/rebuild", AuthHandler("g:server:rebuild"), handlePostServerRebuild)
	api.router.POST("/servers/:server/password", AuthHandler(""), handlePostServerPassword)
	api.router.POST("/servers/:server/power", AuthHandler("s:power"), handlePostServerPower)
	api.router.POST("/servers/:server/command", AuthHandler("s:command"), handlePostServerCommand)
	api.router.GET("/servers/:server/log", AuthHandler("s:console"), handleGetServerLog)
	api.router.POST("/servers/:server/suspend", AuthHandler(""), handlePostServerSuspend)
	api.router.POST("/servers/:server/unsuspend", AuthHandler(""), handlePostServerUnsuspend)
}
