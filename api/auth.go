package api

import (
	"net/http"

	"github.com/Pterodactyl/wings/config"
	"github.com/Pterodactyl/wings/control"
	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"
)

const (
	accessTokenHeader  = "X-Access-Token"
	accessServerHeader = "X-Access-Server"

	// ContextVarServer is the gin.Context field containing the requested server (gin.Context.Get())
	ContextVarServer = "server"
	// ContextVarAuth is the gin.Context field containing the authorizationManager
	// for the request (gin.Context.Get())
	ContextVarAuth = "auth"
)

type responseError struct {
	Error string `json:"error"`
}

// AuthorizationManager handles permission checks
type AuthorizationManager interface {
	hasPermission(string) bool
}

type authorizationManager struct {
	token  string
	server control.Server
}

var _ AuthorizationManager = &authorizationManager{}

func newAuthorizationManager(token string, server control.Server) *authorizationManager {
	return &authorizationManager{
		token:  token,
		server: server,
	}
}

func (a *authorizationManager) hasPermission(permission string) bool {
	if permission == "" {
		return true
	}
	prefix := permission[:1]
	if prefix == "c" {
		return config.Get().ContainsAuthKey(a.token)
	}
	if a.server == nil {
		return false
	}
	if prefix == "g" {
		return config.Get().ContainsAuthKey(a.token)
	}
	if prefix == "s" {
		return a.server.HasPermission(a.token, permission)
	}
	return false
}

// AuthHandler returns a HandlerFunc that checks request authentication
// permission is a permission string describing the required permission to access the route
func AuthHandler(permission string) gin.HandlerFunc {
	return func(c *gin.Context) {
		requestToken := c.Request.Header.Get(accessTokenHeader)
		requestServer := c.Request.Header.Get(accessServerHeader)
		var server control.Server

		if requestToken == "" {
			log.Debug("Token missing in request.")
			c.JSON(http.StatusBadRequest, responseError{"Missing required " + accessTokenHeader + " header."})
			c.Abort()
			return
		}
		if requestServer != "" {
			server = control.GetServer(requestServer)
			//fmt.Println(server)
			if server == nil {
				log.WithField("serverUUID", requestServer).Error("Auth: Requested server not found.")
			}
		}

		auth := newAuthorizationManager(requestToken, server)

		if auth.hasPermission(permission) {
			c.Set(ContextVarServer, server)
			c.Set(ContextVarAuth, auth)
			return
		}

		c.JSON(http.StatusForbidden, responseError{"You do not have permission to perform this action."})
		c.Abort()
	}
}
