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
	listener := fmt.Sprintf("%s:%d", viper.GetString(config.APIHost), viper.GetInt(config.APIPort))

	if !viper.GetBool(config.Debug) {
		gin.SetMode(gin.ReleaseMode)
	}

	api.router = gin.Default()
	api.RegisterRoutes()

	api.router.Run(listener)
	log.Info("Now listening on %s", listener)
	log.Fatal(http.ListenAndServe(listener, nil))
}
