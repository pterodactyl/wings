package api

import (
	"github.com/gin-gonic/gin"
	"github.com/pterodactyl/wings/control"
)

func getServerFromContext(context *gin.Context) control.Server {
	return control.GetServer(context.Param("server"))
}
