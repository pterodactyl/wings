package api

import (
	"github.com/gin-gonic/gin"
)

func (api *API) registerRoutes() {
	api.router.GET("/", AuthHandler(""), handleGetIndex)
	api.router.PUT("/config", AuthHandler("c:config"), handlePutConfig)
	api.router.PATCH("/config", AuthHandler("c:config"), handlePatchConfig)

	api.registerServerRoutes()
	api.registerServerFileRoutes()
}

func handle(c *gin.Context) {

}
