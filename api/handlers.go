package api

import (
	"net/http"

	"github.com/Pterodactyl/wings/constants"
	"github.com/gin-gonic/gin"
)

// handleGetIndex handles GET /
func handleGetIndex(c *gin.Context) {
	auth, _ := c.Get(ContextVarAuth)

	if auth := auth.(AuthorizationManager); auth.hasPermission("c:info") {

	}

	c.Header("Content-Type", "text/html")
	c.String(http.StatusOK, constants.IndexPage)
}

// handlePutConfig handles PUT /config
func handlePutConfig(c *gin.Context) {

}

// handlePatchConfig handles PATCH /config
func handlePatchConfig(c *gin.Context) {

}
