package router

import (
	"bytes"
	"github.com/apex/log"
	"github.com/gin-gonic/gin"
	"github.com/pterodactyl/wings/config"
	"github.com/pterodactyl/wings/installer"
	"github.com/pterodactyl/wings/server"
	"github.com/pterodactyl/wings/system"
	"net/http"
)

// Returns information about the system that wings is running on.
func getSystemInformation(c *gin.Context) {
	i, err := system.GetSystemInformation()
	if err != nil {
		TrackedError(err).AbortWithServerError(c)

		return
	}

	c.JSON(http.StatusOK, i)
}

// Returns all of the servers that are registered and configured correctly on
// this wings instance.
func getAllServers(c *gin.Context) {
	c.JSON(http.StatusOK, server.GetServers().All())
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

		TrackedError(err).AbortWithServerError(c)
		return
	}

	// Plop that server instance onto the request so that it can be referenced in
	// requests from here-on out.
	server.GetServers().Add(install.Server())

	// Begin the installation process in the background to not block the request
	// cycle. If there are any errors they will be logged and communicated back
	// to the Panel where a reinstall may take place.
	go func(i *installer.Installer) {
		i.Execute()

		if err := i.Server().Install(); err != nil {
			log.WithFields(log.Fields{"server": i.Uuid(), "error": err}).Error("failed to run install process for server")
		}
	}(install)

	c.Status(http.StatusAccepted)
}

// Updates the running configuration for this daemon instance.
func postUpdateConfiguration(c *gin.Context) {
	// A backup of the configuration for error purposes.
	ccopy := *config.Get()
	// A copy of the configuration we're using to bind the data recevied into.
	cfg := *config.Get()

	// BindJSON sends 400 if the request fails, all we need to do is return
	if err := c.BindJSON(&cfg); err != nil {
		return
	}

	config.Set(&cfg)
	if err := config.Get().WriteToDisk(); err != nil {
		// If there was an error writing to the disk, revert back to the configuration we had
		// before this code was run.
		config.Set(&ccopy)

		TrackedError(err).AbortWithServerError(c)
		return
	}

	c.Status(http.StatusNoContent)
}
