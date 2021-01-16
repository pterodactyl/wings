package router

import (
	"net/http"
	"strings"

	"emperror.dev/errors"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/pterodactyl/wings/config"
	"github.com/pterodactyl/wings/server"
)

type Middleware struct{}

// RequireAuthorization authenticates the request token against the given
// permission string, ensuring that if it is a server permission, the token has
// control over that server. If it is a global token, this will ensure that the
// request is using a properly signed global token.
func (m *Middleware) RequireAuthorization() gin.HandlerFunc {
	token := config.Get().AuthenticationToken
	return func(c *gin.Context) {
		auth := strings.SplitN(c.GetHeader("Authorization"), " ", 2)
		if len(auth) != 2 || auth[0] != "Bearer" {
			c.Header("WWW-Authenticate", "Bearer")
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"error": "The required authorization heads were not present in the request.",
			})
			return
		}

		// All requests to Wings must be authorized with the authentication token present in
		// the Wings configuration file. Remeber, all requests to Wings come from the Panel
		// backend, or using a signed JWT for temporary authentication.
		if auth[1] == token {
			c.Next()
			return
		}
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
			"error": "You are not authorized to access this endpoint.",
		})
	}
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
func (m *Middleware) ServerExists() gin.HandlerFunc {
	return func(c *gin.Context) {
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
}

// Checks if remote file downloading is enabled on this instance before allowing access
// to the given endpoint.
func (m *Middleware) CheckRemoteDownloadEnabled() gin.HandlerFunc {
	disabled := config.Get().Api.DisableRemoteDownload
	return func(c *gin.Context) {
		if disabled {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
				"error": "This functionality is not currently enabled on this instance.",
			})
			return
		}
		c.Next()
	}
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
