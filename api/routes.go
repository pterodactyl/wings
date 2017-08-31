package api

func (api *API) registerRoutes() {
	api.router.GET("/", AuthHandler(""), handleGetIndex)
	api.router.PATCH("/config", AuthHandler("c:config"), handlePatchConfig)

	api.registerServerRoutes()
	api.registerServerFileRoutes()
}
