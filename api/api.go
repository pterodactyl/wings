package api

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"
	"github.com/spf13/viper"

	"github.com/Pterodactyl/wings/config"
)

type InternalAPI struct {
	router *gin.Engine
}

func NewAPI() InternalAPI {
	return InternalAPI{}
}

// Configure the API and begin listening on the configured IP and Port.
func (api *InternalAPI) Listen() {
	if !viper.GetBool(config.Debug) {
		gin.SetMode(gin.ReleaseMode)
	}

	api.router = gin.Default()
	api.router.RedirectTrailingSlash = false

	api.router.Use(func(c *gin.Context) {
		c.Header("Access-Control-Allow-Origin", "*")
	})

	api.router.OPTIONS("/", func(c *gin.Context) {
		c.Header("Access-Control-Allow-Methods", "POST, GET, PUT, DELETE, OPTIONS")
		c.Header("Access-Control-Allow-Headers", "X-Access-Token")
	})

	api.RegisterRoutes()

	listenString := fmt.Sprintf("%s:%d", viper.GetString(config.APIHost), viper.GetInt(config.APIPort))

	api.router.Run(listenString)

	log.Info("Now listening on %s", listenString)
	log.Fatal(http.ListenAndServe(listenString, nil))
}
