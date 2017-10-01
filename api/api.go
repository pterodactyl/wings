package api

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"
	"github.com/spf13/viper"

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
	if !viper.GetBool(config.Debug) {
		gin.SetMode(gin.ReleaseMode)
	}

	api.router = gin.Default()

	api.router.Use(func(c *gin.Context) {
		c.Header("Access-Control-Allow-Origin", "*")
	})

	api.router.OPTIONS("/", func(c *gin.Context) {
		c.Header("Access-Control-Allow-Methods", "POST, GET, PUT, DELETE, OPTIONS")
		c.Header("Access-Control-Allow-Headers", "X-Access-Token")
	})

	api.registerRoutes()

	listenString := fmt.Sprintf("%s:%d", viper.GetString(config.APIHost), viper.GetInt(config.APIPort))

	api.router.Run(listenString)

	log.Info("Now listening on %s", listenString)
	log.Fatal(http.ListenAndServe(listenString, nil))
}

func getRoot(c *gin.Context) {
	c.String(http.StatusOK, "hello!")
}
