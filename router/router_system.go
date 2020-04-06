package router

import (
	"github.com/gin-gonic/gin"
	"github.com/pterodactyl/wings/installer"
	"github.com/pterodactyl/wings/server"
	"github.com/pterodactyl/wings/system"
	"go.uber.org/zap"
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
	var data []byte
	c.Bind(&data)

	install, err := installer.New(data)
	if err != nil {
		TrackedError(err).
			SetMessage("Failed to validate the data provided in the request.").
			AbortWithStatus(http.StatusUnprocessableEntity, c)
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
			zap.S().Errorw(
				"failed to run install process for server",
				zap.String("server", i.Uuid()),
				zap.Error(err),
			)
		}
	}(install)

	c.Status(http.StatusAccepted)
}