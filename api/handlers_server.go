package api

import (
	"net/http"

	"github.com/Pterodactyl/wings/control"
	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"
)

// GET /servers
// TODO: make jsonapi compliant
func handleGetServers(c *gin.Context) {
	servers := control.GetServers()
	c.JSON(http.StatusOK, servers)
}

// POST /servers
// TODO: make jsonapi compliant
func handlePostServers(c *gin.Context) {
	server := control.ServerStruct{}
	if err := c.BindJSON(&server); err != nil {
		log.WithField("server", server).WithError(err).Error("Failed to parse server request.")
		c.Status(http.StatusBadRequest)
		return
	}
	var srv control.Server
	var err error
	if srv, err = control.CreateServer(&server); err != nil {
		if _, ok := err.(control.ErrServerExists); ok {
			log.WithError(err).Error("Cannot create server, it already exists.")
			c.Status(http.StatusBadRequest)
			return
		}
		log.WithField("server", server).WithError(err).Error("Failed to create server.")
		c.Status(http.StatusInternalServerError)
		return
	}
	go func() {
		env, err := srv.Environment()
		if err != nil {
			log.WithField("server", srv).WithError(err).Error("Failed to get server environment.")
		}
		env.Create()
	}()
	c.JSON(http.StatusOK, srv)
}

// GET /servers/:server
// TODO: make jsonapi compliant
func handleGetServer(c *gin.Context) {
	id := c.Param("server")
	server := control.GetServer(id)
	if server == nil {
		c.Status(http.StatusNotFound)
		return
	}
	c.JSON(http.StatusOK, server)
}

// PATCH /servers/:server
func handlePatchServer(c *gin.Context) {

}

// DELETE /servers/:server
// TODO: make jsonapi compliant
func handleDeleteServer(c *gin.Context) {
	id := c.Param("server")
	server := control.GetServer(id)
	if server == nil {
		c.Status(http.StatusNotFound)
		return
	}

	env, err := server.Environment()
	if err != nil {
		log.WithError(err).WithField("server", server).Error("Failed to delete server.")
	}
	if err := env.Destroy(); err != nil {
		log.WithError(err).Error("Failed to delete server, the environment couldn't be destroyed.")
	}

	if err := control.DeleteServer(id); err != nil {
		log.WithError(err).Error("Failed to delete server.")
		c.Status(http.StatusInternalServerError)
		return
	}
	c.Status(http.StatusOK)
}

func handlePostServerReinstall(c *gin.Context) {

}

func handlePostServerPassword(c *gin.Context) {

}

func handlePostServerRebuild(c *gin.Context) {

}

// POST /servers/:server/power
// TODO: make jsonapi compliant
func handlePostServerPower(c *gin.Context) {
	server := getServerFromContext(c)
	if server == nil {
		c.Status(http.StatusNotFound)
		return
	}

	auth := GetContextAuthManager(c)
	if auth == nil {
		c.Status(http.StatusInternalServerError)
		return
	}

	switch c.Query("action") {
	case "start":
		{
			if !auth.HasPermission("s:power:start") {
				c.Status(http.StatusForbidden)
				return
			}
			server.Start()
		}
	case "stop":
		{
			if !auth.HasPermission("s:power:stop") {
				c.Status(http.StatusForbidden)
				return
			}
			server.Stop()
		}
	case "restart":
		{
			if !auth.HasPermission("s:power:restart") {
				c.Status(http.StatusForbidden)
				return
			}
			server.Restart()
		}
	case "kill":
		{
			if !auth.HasPermission("s:power:kill") {
				c.Status(http.StatusForbidden)
				return
			}
			server.Kill()
		}
	default:
		{
			c.Status(http.StatusBadRequest)
		}
	}
}

// POST /servers/:server/command
// TODO: make jsonapi compliant
func handlePostServerCommand(c *gin.Context) {
	server := getServerFromContext(c)
	cmd := c.Query("command")
	server.Exec(cmd)
}

func handleGetServerLog(c *gin.Context) {

}

func handlePostServerSuspend(c *gin.Context) {

}

func handlePostServerUnsuspend(c *gin.Context) {

}
