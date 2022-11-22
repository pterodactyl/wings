package middleware

import (
	"crypto/subtle"
	"io"
	"net/http"
	"strings"

	"emperror.dev/errors"
	"github.com/apex/log"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/pterodactyl/wings/config"
	"github.com/pterodactyl/wings/remote"
	"github.com/pterodactyl/wings/server"
)

// AttachRequestID attaches a unique ID to the incoming HTTP request so that any
// errors that are generated or returned to the client will include this reference
// allowing for an easier time identifying the specific request that failed for
// the user.
//
// If you are using a tool such as Sentry or Bugsnag for error reporting this is
// a great location to also attach this request ID to your error handling logic
// so that you can easily cross-reference the errors.
func AttachRequestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		id := uuid.New().String()
		c.Set("request_id", id)
		c.Set("logger", log.WithField("request_id", id))
		c.Header("X-Request-Id", id)
		c.Next()
	}
}

// AttachServerManager attaches the server manager to the request context which
// allows routes to access the underlying server collection.
func AttachServerManager(m *server.Manager) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Set("manager", m)
		c.Next()
	}
}

// AttachApiClient attaches the application API client which allows routes to
// access server resources from the Panel easily.
func AttachApiClient(client remote.Client) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Set("api_client", client)
		c.Next()
	}
}

// CaptureAndAbort aborts the request and attaches the provided error to the gin
// context, so it can be reported properly. If the error is missing a stacktrace
// at the time it is called the stack will be attached.
func CaptureAndAbort(c *gin.Context, err error) {
	c.Abort()
	c.Error(errors.WithStackDepthIf(err, 1))
}

// CaptureErrors is custom handler function allowing for errors bubbled up by
// c.Error() to be returned in a standardized format with tracking UUIDs on them
// for easier log searching.
func CaptureErrors() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Next()
		err := c.Errors.Last()
		if err == nil || err.Err == nil {
			return
		}

		status := http.StatusInternalServerError
		if c.Writer.Status() != 200 {
			status = c.Writer.Status()
		}
		if err.Error() == io.EOF.Error() {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "The data passed in the request was not in a parsable format. Please try again."})
			return
		}
		captured := NewError(err.Err)
		if status, msg := captured.asFilesystemError(); msg != "" {
			c.AbortWithStatusJSON(status, gin.H{"error": msg, "request_id": c.Writer.Header().Get("X-Request-Id")})
			return
		}
		captured.Abort(c, status)
	}
}

// SetAccessControlHeaders sets the access request control headers on all of
// the requests.
func SetAccessControlHeaders() gin.HandlerFunc {
	cfg := config.Get()
	origins := cfg.AllowedOrigins
	location := cfg.PanelLocation
	allowPrivateNetwork := cfg.AllowCORSPrivateNetwork

	return func(c *gin.Context) {
		c.Header("Access-Control-Allow-Origin", location)
		c.Header("Access-Control-Allow-Credentials", "true")
		c.Header("Access-Control-Allow-Methods", "GET, POST, PATCH, PUT, DELETE, OPTIONS")
		c.Header("Access-Control-Allow-Headers", "Accept, Accept-Encoding, Authorization, Cache-Control, Content-Type, Content-Length, Origin, X-Real-IP, X-CSRF-Token")

		// CORS for Private Networks (RFC1918)
		// @see https://developer.chrome.com/blog/private-network-access-update/?utm_source=devtools
		if allowPrivateNetwork {
			c.Header("Access-Control-Request-Private-Network", "true")
		}

		// Maximum age allowable under Chromium v76 is 2 hours, so just use that since
		// anything higher will be ignored (even if other browsers do allow higher values).
		//
		// @see https://developer.mozilla.org/en-US/docs/Web/HTTP/Headers/Access-Control-Max-Age#Directives
		c.Header("Access-Control-Max-Age", "7200")

		// Validate that the request origin is coming from an allowed origin. Because you
		// cannot set multiple values here we need to see if the origin is one of the ones
		// that we allow, and if so return it explicitly. Otherwise, just return the default
		// origin which is the same URL that the Panel is located at.
		origin := c.GetHeader("Origin")
		if origin != location {
			for _, o := range origins {
				if o != "*" && o != origin {
					continue
				}
				c.Header("Access-Control-Allow-Origin", o)
				break
			}
		}
		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}

// ServerExists will ensure that the requested server exists in this setup.
// Returns a 404 if we cannot locate it. If the server is found it is set into
// the request context, and the logger for the context is also updated to include
// the server ID in the fields list.
func ServerExists() gin.HandlerFunc {
	return func(c *gin.Context) {
		var s *server.Server
		if c.Param("server") != "" {
			manager := ExtractManager(c)
			s = manager.Find(func(s *server.Server) bool {
				return c.Param("server") == s.ID()
			})
		}
		if s == nil {
			c.AbortWithStatusJSON(http.StatusNotFound, gin.H{"error": "The requested resource does not exist on this instance."})
			return
		}
		c.Set("logger", ExtractLogger(c).WithField("server_id", s.ID()))
		c.Set("server", s)
		c.Next()
	}
}

// RequireAuthorization authenticates the request token against the given
// permission string, ensuring that if it is a server permission, the token has
// control over that server. If it is a global token, this will ensure that the
// request is using a properly signed global token.
func RequireAuthorization() gin.HandlerFunc {
	return func(c *gin.Context) {
		// We don't put this value outside this function since the node's authentication
		// token can be changed on the fly and the config.Get() call returns a copy, so
		// if it is rotated this value will never properly get updated.
		token := config.Get().AuthenticationToken
		auth := strings.SplitN(c.GetHeader("Authorization"), " ", 2)
		if len(auth) != 2 || auth[0] != "Bearer" {
			c.Header("WWW-Authenticate", "Bearer")
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "The required authorization heads were not present in the request."})
			return
		}

		// All requests to Wings must be authorized with the authentication token present in
		// the Wings configuration file. Remeber, all requests to Wings come from the Panel
		// backend, or using a signed JWT for temporary authentication.
		if subtle.ConstantTimeCompare([]byte(auth[1]), []byte(token)) != 1 {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "You are not authorized to access this endpoint."})
			return
		}
		c.Next()
	}
}

// RemoteDownloadEnabled checks if remote downloads are enabled for this instance
// and if not aborts the request.
func RemoteDownloadEnabled() gin.HandlerFunc {
	disabled := config.Get().Api.DisableRemoteDownload
	return func(c *gin.Context) {
		if disabled {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "This functionality is not currently enabled on this instance."})
			return
		}
		c.Next()
	}
}

// ExtractLogger pulls the logger out of the request context and returns it. By
// default this will include the request ID, but may also include the server ID
// if that middleware has been used in the chain by the time it is called.
func ExtractLogger(c *gin.Context) *log.Entry {
	v, ok := c.Get("logger")
	if !ok {
		panic("middleware/middleware: cannot extract logger: not present in request context")
	}
	return v.(*log.Entry)
}

// ExtractServer will return the server from the gin.Context or panic if it is
// not present.
func ExtractServer(c *gin.Context) *server.Server {
	v, ok := c.Get("server")
	if !ok {
		panic("middleware/middleware: cannot extract server: not present in request context")
	}
	return v.(*server.Server)
}

// ExtractApiClient returns the API client defined for the routes.
func ExtractApiClient(c *gin.Context) remote.Client {
	if v, ok := c.Get("api_client"); ok {
		return v.(remote.Client)
	}
	panic("middleware/middlware: cannot extract api clinet: not present in context")
}

// ExtractManager returns the server manager instance set on the request context.
func ExtractManager(c *gin.Context) *server.Manager {
	if v, ok := c.Get("manager"); ok {
		return v.(*server.Manager)
	}
	panic("middleware/middleware: cannot extract server manager: not present in context")
}
