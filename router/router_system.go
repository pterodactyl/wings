package router

import (
	"bytes"
	"net/http"
	"strings"

	"github.com/apex/log"
	"github.com/gin-gonic/gin"
	"github.com/pterodactyl/wings/config"
	"github.com/pterodactyl/wings/installer"
	"github.com/pterodactyl/wings/router/middleware"
	"github.com/pterodactyl/wings/system"
)

// Returns information about the system that wings is running on.
func getSystemInformation(c *gin.Context) {
	i, err := system.GetSystemInformation()
	if err != nil {
		NewTrackedError(err).Abort(c)

		return
	}

	c.JSON(http.StatusOK, i)
}

// Returns all of the servers that are registered and configured correctly on
// this wings instance.
func getAllServers(c *gin.Context) {
	c.JSON(http.StatusOK, middleware.ExtractManager(c).All())
}

// Creates a new server on the wings daemon and begins the installation process
// for it.
func postCreateServer(c *gin.Context) {
	buf := bytes.Buffer{}
	buf.ReadFrom(c.Request.Body)

	install, err := installer.New(buf.Bytes())
	if err != nil {
		if installer.IsValidationError(err) {
			c.AbortWithStatusJSON(http.StatusUnprocessableEntity, gin.H{
				"error": "The data provided in the request could not be validated.",
			})
			return
		}

		NewTrackedError(err).Abort(c)
		return
	}

	// Plop that server instance onto the request so that it can be referenced in
	// requests from here-on out.
	manager := middleware.ExtractManager(c)
	manager.Add(install.Server())

	// Begin the installation process in the background to not block the request
	// cycle. If there are any errors they will be logged and communicated back
	// to the Panel where a reinstall may take place.
	go func(i *installer.Installer) {
		err := i.Server().CreateEnvironment()
		if err != nil {
			i.Server().Log().WithField("error", err).Error("failed to create server environment during install process")
			return
		}

		if err := i.Server().Install(false); err != nil {
			log.WithFields(log.Fields{"server": i.Uuid(), "error": err}).Error("failed to run install process for server")
		}
	}(install)

	c.Status(http.StatusAccepted)
}

// Updates the running configuration for this Wings instance.
func postUpdateConfiguration(c *gin.Context) {
	cfg := config.Get()
	if err := c.BindJSON(&cfg); err != nil {
		return
	}
	// Keep the SSL certificates the same since the Panel will send through Lets Encrypt
	// default locations. However, if we picked a different location manually we don't
	// want to override that.
	//
	// If you pass through manual locations in the API call this logic will be skipped.
	if strings.HasPrefix(cfg.Api.Ssl.KeyFile, "/etc/letsencrypt/live/") {
		cfg.Api.Ssl.KeyFile = strings.ToLower(config.Get().Api.Ssl.KeyFile)
		cfg.Api.Ssl.CertificateFile = strings.ToLower(config.Get().Api.Ssl.CertificateFile)
	}
	// Try to write this new configuration to the disk before updating our global
	// state with it.
	if err := config.WriteToDisk(cfg); err != nil {
		WithError(c, err)
		return
	}
	// Since we wrote it to the disk successfully now update the global configuration
	// state to use this new configuration struct.
	config.Set(cfg)
	c.Status(http.StatusNoContent)
}
