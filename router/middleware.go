package router

import (
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/pterodactyl/wings/config"
	"github.com/pterodactyl/wings/server"
	"net/http"
	"strings"
)

// Set the access request control headers on all of the requests.
func SetAccessControlHeaders(c *gin.Context) {
	c.Header("Access-Control-Allow-Origin", config.Get().PanelLocation)
	c.Header("Access-Control-Allow-Headers", "Accept, Content-Type, Content-Length, Accept-Encoding, X-CSRF-Token, Authorization")
	c.Next()
}

// Authenticates the request token against the given permission string, ensuring that
// if it is a server permission, the token has control over that server. If it is a global
// token, this will ensure that the request is using a properly signed global token.
func AuthorizationMiddleware(c *gin.Context) {
	auth := strings.SplitN(c.GetHeader("Authorization"), " ", 2)

	if len(auth) != 2 || auth[0] != "Bearer" {
		c.Header("WWW-Authenticate", "Bearer")
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
			"error": "The required authorization heads were not present in the request.",
		})

		return
	}

	// Try to match the request against the global token for the Daemon, regardless
	// of the permission type. If nothing is matched we will fall through to the Panel
	// API to try and validate permissions for a server.
	if auth[1] == config.Get().AuthenticationToken {
		c.Next()

		return
	}

	c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
		"error": "You are not authorized to access this endpoint.",
	})
}

// Helper function to fetch a server out of the servers collection stored in memory.
func GetServer(uuid string) *server.Server {
	return server.GetServers().Find(func(s *server.Server) bool {
		return uuid == s.Uuid
	})
}

// Ensure that the requested server exists in this setup. Returns a 404 if we cannot
// locate it.
func ServerExists(c *gin.Context) {
	u, err := uuid.Parse(c.Param("server"))
	if err != nil || GetServer(u.String()) == nil {
		c.AbortWithStatusJSON(http.StatusNotFound, gin.H{
			"error": "The requested server does not exist.",
		})
		return
	}

	c.Next()
}
