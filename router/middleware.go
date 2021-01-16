package router

import (
	"github.com/gin-gonic/gin"
	"github.com/pterodactyl/wings/router/middleware"
	"github.com/pterodactyl/wings/server"
)

// GetServer is a helper function to fetch a server out of the servers
// collection stored in memory. This function should not be used in new
// controllers, prefer ExtractServer where possible.
// Deprecated
func GetServer(uuid string) *server.Server {
	return server.GetServers().Find(func(s *server.Server) bool {
		return uuid == s.Id()
	})
}

// ExtractServer returns the server instance from the gin context. If there is
// no server set in the context (e.g. calling from a controller not protected by
// ServerExists) this function will panic.
// Deprecated
func ExtractServer(c *gin.Context) *server.Server {
	return middleware.ExtractServer(c)
}
