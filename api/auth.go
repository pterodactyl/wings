package api

import (
	"net/http"

	"github.com/Pterodactyl/wings/config"
	"github.com/Pterodactyl/wings/control"
	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"
)

const (
	accessTokenHeader = "X-Access-Token"

	contextVarServer = "server"
	contextVarAuth   = "auth"
)

type responseError struct {
	Error string `json:"error"`
}

// AuthorizationManager handles permission checks
type AuthorizationManager interface {
	HasPermission(string) bool
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

func (a *authorizationManager) HasPermission(permission string) bool {
	if permission == "" {
		return true
	}
	prefix := permission[:1]
	if prefix == "c" {
		return config.ContainsAuthKey(a.token)
	}
	if a.server == nil {
		log.WithField("permission", permission).Error("Auth: Server required but none found.")
		return false
	}
	if prefix == "g" {
		return config.ContainsAuthKey(a.token)
	}
	if prefix == "s" {
		return a.server.HasPermission(a.token, permission) || config.ContainsAuthKey(a.token)
	}
	return false
}

// AuthHandler returns a HandlerFunc that checks request authentication
// permission is a permission string describing the required permission to access the route
func AuthHandler(permission string) gin.HandlerFunc {
	return func(c *gin.Context) {
		requestToken := c.Request.Header.Get(accessTokenHeader)
		requestServer := c.Param("server")
		var server control.Server

		if requestToken == "" && permission != "" {
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

		if auth.HasPermission(permission) {
			c.Set(contextVarServer, server)
			c.Set(contextVarAuth, auth)
			return
		}

		c.JSON(http.StatusForbidden, responseError{"You do not have permission to perform this action."})
		c.Abort()
	}
}

// GetContextAuthManager returns a AuthorizationManager contained in
// a gin.Context or nil
func GetContextAuthManager(c *gin.Context) AuthorizationManager {
	auth, exists := c.Get(contextVarAuth)
	if !exists {
		return nil
	}
	if auth, ok := auth.(AuthorizationManager); ok {
		return auth
	}
	return nil
}

// GetContextServer returns a control.Server contained in a gin.Context
// or null
func GetContextServer(c *gin.Context) control.Server {
	server, exists := c.Get(contextVarAuth)
	if !exists {
		return nil
	}
	if server, ok := server.(control.Server); ok {
		return server
	}
	return nil
}
