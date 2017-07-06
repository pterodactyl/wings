package api

import (
	"net/http"
	"strings"

	"github.com/Pterodactyl/wings/config"
	"github.com/Pterodactyl/wings/control"
	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"
)

const (
	accessTokenHeader  = "X-Access-Token"
	accessServerHeader = "X-Access-Server"
)

type responseError struct {
	Error string `json:"error"`
}

// AuthHandler returns a HandlerFunc that checks request authentication
// permission is a permission string describing the required permission to access the route
func AuthHandler(permission string) gin.HandlerFunc {
	return func(c *gin.Context) {
		requestToken := c.Request.Header.Get(accessTokenHeader)
		requestServer := c.Request.Header.Get(accessServerHeader)

		if requestToken != "" {
			// c: master controller, permissions not related to specific server
			if strings.HasPrefix(permission, "c:") {
				if config.Get().ContainsAuthKey(requestToken) {
					return
				}
			} else {
				// All other permission strings not starting with c: require a server to be provided
				if requestServer != "" {
					server := control.GetServer(requestServer)
					if server != nil {
						if strings.HasPrefix(permission, "g:") {
							if config.Get().ContainsAuthKey(requestToken) {
								return
							}
						}

						if strings.HasPrefix(permission, "s:") {
							if server.HasPermission(requestToken, permission) {
								return
							}
						}
					} else {
						c.JSON(http.StatusNotFound, responseError{"Server defined in " + accessServerHeader + " is not known."})
					}
				} else {
					c.JSON(http.StatusBadRequest, responseError{"No " + accessServerHeader + " header provided."})
				}
			}
		} else {
			log.Debug("Token missing in request.")
			c.JSON(http.StatusBadRequest, responseError{"No " + accessTokenHeader + " header provided."})
		}
		c.JSON(http.StatusForbidden, responseError{"You are do not have permission to perform this action."})
		c.Abort()
	}
}
