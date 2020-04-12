package router

import (
	"fmt"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/pkg/errors"
	"github.com/pterodactyl/wings/server"
	"go.uber.org/zap"
	"net/http"
	"os"
)

type RequestError struct {
	Err     error
	Uuid    string
	Message string
	server  *server.Server
}

// Generates a new tracked error, which simply tracks the specific error that
// is being passed in, and also assigned a UUID to the error so that it can be
// cross referenced in the logs.
func TrackedError(err error) *RequestError {
	return &RequestError{
		Err:     err,
		Uuid:    uuid.Must(uuid.NewRandom()).String(),
		Message: "",
	}
}

// Same as TrackedError, except this will also attach the server instance that
// generated this server for the purposes of logging.
func TrackedServerError(err error, s *server.Server) *RequestError {
	return &RequestError{
		Err:     err,
		Uuid:    uuid.Must(uuid.NewRandom()).String(),
		Message: "",
		server:  s,
	}
}

// Sets the output message to display to the user in the error.
func (e *RequestError) SetMessage(msg string) *RequestError {
	e.Message = msg
	return e
}

// Aborts the request with the given status code, and responds with the error. This
// will also include the error UUID in the output so that the user can report that
// and link the response to a specific error in the logs.
func (e *RequestError) AbortWithStatus(status int, c *gin.Context) {
	// If this error is because the resource does not exist, we likely do not need to log
	// the error anywhere, just return a 404 and move on with our lives.
	if os.IsNotExist(e.Err) {
		c.AbortWithStatusJSON(http.StatusNotFound, gin.H{
			"error": "The requested resource was not found on the system.",
		})
		return
	}

	// Otherwise, log the error to zap, and then report the error back to the user.
	if status >= 500 {
		if e.server != nil {
			zap.S().Errorw("encountered error while handling HTTP request", zap.String("server", e.server.Uuid), zap.String("error_id", e.Uuid), zap.Error(e.Err))
		} else {
			zap.S().Errorw("encountered error while handling HTTP request", zap.String("error_id", e.Uuid), zap.Error(e.Err))
		}

		c.Error(errors.WithStack(e))
	} else {
		if e.server != nil {
			zap.S().Debugw("encountered error while handling HTTP request", zap.String("server", e.server.Uuid), zap.String("error_id", e.Uuid), zap.Error(e.Err))
		} else {
			zap.S().Debugw("encountered error while handling HTTP request", zap.String("error_id", e.Uuid), zap.Error(e.Err))
		}
	}

	msg := "An unexpected error was encountered while processing this request."
	if e.Message != "" {
		msg = e.Message
	}

	c.AbortWithStatusJSON(status, gin.H{
		"error":    msg,
		"error_id": e.Uuid,
	})
}

// Helper function to just abort with an internal server error. This is generally the response
// from most errors encountered by the API.
func (e *RequestError) AbortWithServerError(c *gin.Context) {
	e.AbortWithStatus(http.StatusInternalServerError, c)
}

// Format the error to a string and include the UUID.
func (e *RequestError) Error() string {
	return fmt.Sprintf("%v (uuid: %s)", e.Err, e.Uuid)
}
