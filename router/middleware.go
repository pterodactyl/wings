package router

import (
	"errors"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/pterodactyl/wings/config"
	"github.com/pterodactyl/wings/server"
	"net/http"
	"strings"
)

// Set the access request control headers on all of the requests.
func SetAccessControlHeaders(c *gin.Context) {
	c.Header("Access-Control-Allow-Headers", "Accept, Content-Type, Content-Length, Accept-Encoding, X-CSRF-Token, Authorization")

	o := c.GetHeader("Origin")
	if o != config.Get().PanelLocation {
		for _, origin := range config.Get().AllowedOrigins {
			if origin != "*" && o != origin {
				continue
			}

			c.Header("Access-Control-Allow-Origin", origin)
			c.Next()
			return
		}
	}

	c.Header("Access-Control-Allow-Origin", config.Get().PanelLocation)
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
//
// This function should not be used in new controllers, prefer ExtractServer where
// possible.
func GetServer(uuid string) *server.Server {
	return server.GetServers().Find(func(s *server.Server) bool {
		return uuid == s.Id()
	})
}

// Ensure that the requested server exists in this setup. Returns a 404 if we cannot
// locate it.
func ServerExists(c *gin.Context) {
	u, err := uuid.Parse(c.Param("server"))
	if err == nil {
		if s := GetServer(u.String()); s != nil {
			c.Set("server", s)
			c.Next()
			return
		}
	}
	c.AbortWithStatusJSON(http.StatusNotFound, gin.H{
		"error": "The resource you requested does not exist.",
	})
}

// Returns the server instance from the gin context. If there is no server set in the
// context (e.g. calling from a controller not protected by ServerExists) this function
// will panic.
func ExtractServer(c *gin.Context) *server.Server {
	if s, ok := c.Get("server"); ok {
		return s.(*server.Server)
	}
	panic(errors.New("cannot extract server, missing on gin context"))
}
