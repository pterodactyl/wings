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
	err     error
	uuid    string
	message string
	server  *server.Server
}

// Generates a new tracked error, which simply tracks the specific error that
// is being passed in, and also assigned a UUID to the error so that it can be
// cross referenced in the logs.
func NewTrackedError(err error) *RequestError {
	return &RequestError{
		err:  err,
		uuid: uuid.Must(uuid.NewRandom()).String(),
	}
}

// Same as NewTrackedError, except this will also attach the server instance that
// generated this server for the purposes of logging.
func NewServerError(err error, s *server.Server) *RequestError {
	return &RequestError{
		err:    err,
		uuid:   uuid.Must(uuid.NewRandom()).String(),
		server: s,
	}
}

func (e *RequestError) logger() *log.Entry {
	if e.server != nil {
		return e.server.Log().WithField("error_id", e.uuid)
	}
	return log.WithField("error_id", e.uuid)
}

// Sets the output message to display to the user in the error.
func (e *RequestError) SetMessage(msg string) *RequestError {
	e.message = msg
	return e
}

// Aborts the request with the given status code, and responds with the error. This
// will also include the error UUID in the output so that the user can report that
// and link the response to a specific error in the logs.
func (e *RequestError) AbortWithStatus(status int, c *gin.Context) {
	// In instances where the status has already been set just use that existing status
	// since we cannot change it at this point, and trying to do so will emit a gin warning
	// into the program output.
	if c.Writer.Status() != 200 {
		status = c.Writer.Status()
	}

	// If this error is because the resource does not exist, we likely do not need to log
	// the error anywhere, just return a 404 and move on with our lives.
	if errors.Is(e.err, os.ErrNotExist) {
		e.logger().WithField("error", e.err).Debug("encountered os.IsNotExist error while handling request")
		c.AbortWithStatusJSON(http.StatusNotFound, gin.H{
			"error": "The requested resource was not found on the system.",
		})
		return
	}

	// If this is a Filesystem error just return it without all of the tracking code nonsense
	// since we don't need to be logging it into the logs or anything, its just a normal error
	// that the user can solve on their end.
	if st, msg := e.getAsFilesystemError(); st != 0 {
		c.AbortWithStatusJSON(st, gin.H{"error": msg})
		return
	}

	// Otherwise, log the error to zap, and then report the error back to the user.
	if status >= 500 {
		e.logger().WithField("error", e.err).Error("unexpected error while handling HTTP request")
	} else {
		e.logger().WithField("error", e.err).Debug("non-server error encountered while handling HTTP request")
	}

	if e.message == "" {
		e.message = "An unexpected error was encountered while processing this request."
	}

	c.AbortWithStatusJSON(status, gin.H{"error": e.message, "error_id": e.uuid})
}

// Helper function to just abort with an internal server error. This is generally the response
// from most errors encountered by the API.
func (e *RequestError) Abort(c *gin.Context) {
	e.AbortWithStatus(http.StatusInternalServerError, c)
}

// Looks at the given RequestError and determines if it is a specific filesystem error that
// we can process and return differently for the user.
func (e *RequestError) getAsFilesystemError() (int, string) {
	if errors.Is(e.err, os.ErrNotExist) || filesystem.IsErrorCode(e.err, filesystem.ErrCodePathResolution) {
		return http.StatusNotFound, "The requested resource was not found on the system."
	}
	if filesystem.IsErrorCode(e.err, filesystem.ErrCodeDiskSpace) {
		return http.StatusConflict, "There is not enough disk space available to perform that action."
	}
	if strings.HasSuffix(e.err.Error(), "file name too long") {
		return http.StatusBadRequest, "Cannot perform that action: file name is too long."
	}
	if e, ok := e.err.(*os.SyscallError); ok && e.Syscall == "readdirent" {
		return http.StatusNotFound, "The requested directory does not exist."
	}
	return 0, ""
}

// Handle specific filesystem errors for a server.
func (e *RequestError) AbortFilesystemError(c *gin.Context) {
	e.Abort(c)
}

// Format the error to a string and include the UUID.
func (e *RequestError) Error() string {
	return fmt.Sprintf("%v (uuid: %s)", e.err, e.uuid)
}
