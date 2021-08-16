package router

import (
	"fmt"
	"net/http"
	"os"
	"strings"

	"emperror.dev/errors"
	"github.com/apex/log"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/pterodactyl/wings/server"
	"github.com/pterodactyl/wings/server/filesystem"
)

type RequestError struct {
	err     error
	uuid    string
	message string
	server  *server.Server
}

// Attaches an error to the gin.Context object for the request and ensures that it
// has a proper stacktrace associated with it when doing so.
//
// If you just call c.Error(err) without using this function you'll likely end up
// with an error that has no annotated stack on it.
func WithError(c *gin.Context, err error) error {
	return c.Error(errors.WithStackDepthIf(err, 1))
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
		return e.server.Log().WithField("error_id", e.uuid).WithField("error", e.err)
	}
	return log.WithField("error_id", e.uuid).WithField("error", e.err)
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
		c.AbortWithStatusJSON(http.StatusNotFound, gin.H{
			"error": "The requested resource was not found on the system.",
		})
		return
	}

	if strings.HasPrefix(e.err.Error(), "invalid URL escape") {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
			"error": "Some of the data provided in the request appears to be escaped improperly.",
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
		e.logger().Error("unexpected error while handling HTTP request")
	} else {
		e.logger().Debug("non-server error encountered while handling HTTP request")
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
	// Some external things end up calling fmt.Errorf() on our filesystem errors
	// which ends up just unleashing chaos on the system. For the sake of this
	// fallback to using text checks...
	if filesystem.IsErrorCode(e.err, filesystem.ErrCodeDenylistFile) || strings.Contains(e.err.Error(), "filesystem: file access prohibited") {
		return http.StatusForbidden, "This file cannot be modified: present in egg denylist."
	}
	if filesystem.IsErrorCode(e.err, filesystem.ErrCodePathResolution) || strings.Contains(e.err.Error(), "resolves to a location outside the server root") {
		return http.StatusNotFound, "The requested resource was not found on the system."
	}
	if filesystem.IsErrorCode(e.err, filesystem.ErrCodeIsDirectory) || strings.Contains(e.err.Error(), "filesystem: is a directory") {
		return http.StatusBadRequest, "Cannot perform that action: file is a directory."
	}
	if filesystem.IsErrorCode(e.err, filesystem.ErrCodeDiskSpace) || strings.Contains(e.err.Error(), "filesystem: not enough disk space") {
		return http.StatusBadRequest, "Cannot perform that action: not enough disk space available."
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
