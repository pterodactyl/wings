package router

import (
	"fmt"
	"github.com/apex/log"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/pkg/errors"
	"github.com/pterodactyl/wings/server"
	"github.com/pterodactyl/wings/server/filesystem"
	"net/http"
	"os"
	"strings"
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

func (e *RequestError) logger() *log.Entry {
	if e.server != nil {
		return e.server.Log().WithField("error_id", e.Uuid)
	}

	return log.WithField("error_id", e.Uuid)
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
		e.logger().WithField("error", e.Err).Debug("encountered os.IsNotExist error while handling request")

		c.AbortWithStatusJSON(http.StatusNotFound, gin.H{
			"error": "The requested resource was not found on the system.",
		})
		return
	}

	// Otherwise, log the error to zap, and then report the error back to the user.
	if status >= 500 {
		e.logger().WithField("error", e.Err).Error("encountered HTTP/500 error while handling request")

		c.Error(errors.WithStack(e))
	} else {
		e.logger().WithField("error", e.Err).Debug("encountered non-HTTP/500 error while handling request")
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

// Handle specific filesystem errors for a server.
func (e *RequestError) AbortFilesystemError(c *gin.Context) {
	if errors.Is(e.Err, os.ErrNotExist) {
		c.AbortWithStatusJSON(http.StatusNotFound, gin.H{
			"error": "The requested resource was not found.",
		})
		return
	}

	if errors.Is(e.Err, filesystem.ErrNotEnoughDiskSpace) {
		c.AbortWithStatusJSON(http.StatusConflict, gin.H{
			"error": "There is not enough disk space available to perform that action.",
		})
		return
	}

	if strings.HasSuffix(e.Err.Error(), "file name too long") {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
			"error": "File name is too long.",
		})
		return
	}

	if e, ok := e.Err.(*os.SyscallError); ok && e.Syscall == "readdirent" {
		c.AbortWithStatusJSON(http.StatusNotFound, gin.H{
			"error": "The requested directory does not exist.",
		})
		return
	}

	if strings.HasSuffix(e.Err.Error(), "file name too long") {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
			"error": "Cannot perform that action: file name is too long.",
		})
		return
	}

	e.AbortWithServerError(c)
}

// Format the error to a string and include the UUID.
func (e *RequestError) Error() string {
	return fmt.Sprintf("%v (uuid: %s)", e.Err, e.Uuid)
}
