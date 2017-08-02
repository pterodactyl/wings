package api

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"

	"github.com/Pterodactyl/wings/config"
)

// API is a grouping struct for the api
type API struct {
	router *gin.Engine
}

// NewAPI creates a new Api object
func NewAPI() API {
	return API{}
}

// Listen starts the api http server
func (api *API) Listen() {
	if !config.Get().Debug {
		gin.SetMode(gin.ReleaseMode)
	}

	api.router = gin.Default()

	api.registerRoutes()

	listenString := fmt.Sprintf("%s:%d", config.Get().Web.ListenHost, config.Get().Web.ListenPort)

	api.router.Run(listenString)

	log.Info("Now listening on %s", listenString)
	log.Fatal(http.ListenAndServe(listenString, nil))
}

func getRoot(c *gin.Context) {
	c.String(http.StatusOK, "hello!")
}
