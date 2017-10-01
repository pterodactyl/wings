package api

import (
	"github.com/Pterodactyl/wings/control"
	"github.com/gin-gonic/gin"
)

func getServerFromContext(context *gin.Context) control.Server {
	return control.GetServer(context.Param("server"))
}
