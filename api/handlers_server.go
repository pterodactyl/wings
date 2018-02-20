package api

import (
	"net/http"

	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/google/jsonapi"
	"github.com/pterodactyl/wings/control"
	log "github.com/sirupsen/logrus"
)

// GET /servers
func handleGetServers(c *gin.Context) {
	servers := control.GetServers()
	sendData(c, servers)
}

// POST /servers
func handlePostServers(c *gin.Context) {
	server := control.ServerStruct{}
	if err := c.BindJSON(&server); err != nil {
		log.WithField("server", server).WithError(err).Error("Failed to parse server request.")
		sendErrors(c, http.StatusBadRequest, &jsonapi.ErrorObject{
			Status: strconv.Itoa(http.StatusBadRequest),
			Title:  "The passed server object is invalid.",
		})
		return
	}
	var srv control.Server
	var err error
	if srv, err = control.CreateServer(&server); err != nil {
		if _, ok := err.(control.ErrServerExists); ok {
			log.WithError(err).Error("Cannot create server, it already exists.")
			c.Status(http.StatusBadRequest)
			sendErrors(c, http.StatusConflict, &jsonapi.ErrorObject{
				Status: strconv.Itoa(http.StatusConflict),
				Title:  "A server with this ID already exists.",
			})
			return
		}
		log.WithField("server", server).WithError(err).Error("Failed to create server.")
		sendInternalError(c, "Failed to create the server", "")
		return
	}
	go func() {
		env, err := srv.Environment()
		if err != nil {
			log.WithField("server", srv).WithError(err).Error("Failed to get server environment.")
		}
		env.Create()
	}()
	sendDataStatus(c, http.StatusCreated, srv)
}

// GET /servers/:server
func handleGetServer(c *gin.Context) {
	id := c.Param("server")
	server := control.GetServer(id)
	if server == nil {
		sendErrors(c, http.StatusNotFound, &jsonapi.ErrorObject{
			Code:   strconv.Itoa(http.StatusNotFound),
			Title:  "Server not found.",
			Detail: "The requested Server with the id " + id + " couldn't be found.",
		})
		return
	}
	sendData(c, server)
}

// PATCH /servers/:server
func handlePatchServer(c *gin.Context) {

}

// DELETE /servers/:server
func handleDeleteServer(c *gin.Context) {
	id := c.Param("server")
	server := control.GetServer(id)
	if server == nil {
		c.Status(http.StatusNotFound)
		return
	}

	env, err := server.Environment()
	if err != nil {
		sendInternalError(c, "The server could not be deleted.", "")
		return
	}
	if err := env.Destroy(); err != nil {
		log.WithError(err).Error("Failed to delete server, the environment couldn't be destroyed.")
		sendInternalError(c, "The server could not be deleted.", "The server environment couldn't be destroyed.")
		return
	}

	if err := control.DeleteServer(id); err != nil {
		log.WithError(err).Error("Failed to delete server.")
		sendInternalError(c, "The server could not be deleted.", "")
		return
	}
	c.Status(http.StatusNoContent)
}

func handlePostServerReinstall(c *gin.Context) {

}

func handlePostServerPassword(c *gin.Context) {

}

func handlePostServerRebuild(c *gin.Context) {

}

// POST /servers/:server/power
func handlePostServerPower(c *gin.Context) {
	server := getServerFromContext(c)
	if server == nil {
		c.Status(http.StatusNotFound)
		return
	}

	auth := GetContextAuthManager(c)
	if auth == nil {
		sendInternalError(c, "An internal error occured.", "")
		return
	}

	switch c.Query("action") {
	case "start":
		{
			if !auth.HasPermission("s:power:start") {
				sendForbidden(c)
				return
			}
			server.Start()
		}
	case "stop":
		{
			if !auth.HasPermission("s:power:stop") {
				sendForbidden(c)
				return
			}
			server.Stop()
		}
	case "restart":
		{
			if !auth.HasPermission("s:power:restart") {
				sendForbidden(c)
				return
			}
			server.Restart()
		}
	case "kill":
		{
			if !auth.HasPermission("s:power:kill") {
				sendForbidden(c)
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
func handlePostServerCommand(c *gin.Context) {
	server := getServerFromContext(c)
	cmd := c.Query("command")
	server.Exec(cmd)
	c.Status(204)
}

func handleGetConsole(c *gin.Context) {
	server := getServerFromContext(c)
	server.Websockets().Upgrade(c.Writer, c.Request)
}

func handleGetServerLog(c *gin.Context) {

}

func handlePostServerSuspend(c *gin.Context) {

}

func handlePostServerUnsuspend(c *gin.Context) {

}
