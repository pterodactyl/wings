package api

import (
	"net/http"
    "strings"

    "strconv"

	"github.com/gin-gonic/gin"
	"github.com/google/jsonapi"
	"github.com/pterodactyl/wings/config"
	"github.com/pterodactyl/wings/control"
	log "github.com/sirupsen/logrus"
)

const (
	accessTokenHeader = "Authorization"

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
//
// The AuthHandler looks for an access token header (defined in accessTokenHeader)
// or a `token` request parameter
func AuthHandler(permission string) gin.HandlerFunc {
	return func(c *gin.Context) {
		requestToken := c.Request.Header.Get(accessTokenHeader)
		if requestToken != "" && strings.HasPrefix(requestToken, "Baerer ") {
            requestToken = requestToken[7:]
        } else {
			requestToken = c.Query("token")
		}
		requestServer := c.Param("server")
		var server control.Server

		if requestToken == "" && permission != "" {
			sendErrors(c, http.StatusUnauthorized, &jsonapi.ErrorObject{
				Title:  "Missing required " + accessTokenHeader + " header or token param.",
				Status: strconv.Itoa(http.StatusUnauthorized),
			})
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

		sendForbidden(c)
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
