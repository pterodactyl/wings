package middleware

import (
	"context"
	"net/http"
	"os"
	"strings"

	"emperror.dev/errors"
	"github.com/apex/log"
	"github.com/gin-gonic/gin"

	"github.com/pterodactyl/wings/server"
	"github.com/pterodactyl/wings/server/filesystem"
)

// RequestError is a custom error type returned when something goes wrong with
// any of the HTTP endpoints.
type RequestError struct {
	err    error
	status int
	msg    string
}

// NewError returns a new RequestError for the provided error.
func NewError(err error) *RequestError {
	return &RequestError{
		// Attach a stacktrace to the error if it is missing at this point and mark it
		// as originating from the location where NewError was called, rather than this
		// specific point in the code.
		err: errors.WithStackDepthIf(err, 1),
	}
}

// SetMessage allows for a custom error message to be set on an existing
// RequestError instance.
func (re *RequestError) SetMessage(m string) {
	re.msg = m
}

// SetStatus sets the HTTP status code for the error response. By default this
// is a HTTP-500 error.
func (re *RequestError) SetStatus(s int) {
	re.status = s
}

// Abort aborts the given HTTP request with the specified status code and then
// logs the event into the logs. The error that is output will include the unique
// request ID if it is present.
func (re *RequestError) Abort(c *gin.Context, status int) {
	reqId := c.Writer.Header().Get("X-Request-Id")

	// Generate the base logger instance, attaching the unique request ID and
	// the URL that was requested.
	event := log.WithField("request_id", reqId).WithField("url", c.Request.URL.String())
	// If there is a server present in the gin.Context stack go ahead and pull it
	// and attach that server UUID to the logs as well so that we can see what specific
	// server triggered this error.
	if s, ok := c.Get("server"); ok {
		if s, ok := s.(*server.Server); ok {
			event = event.WithField("server_id", s.ID())
		}
	}

	if c.Writer.Status() == 200 {
		// Handle context deadlines being exceeded a little differently since we want
		// to report a more user-friendly error and a proper error code. The "context
		// canceled" error is generally when a request is terminated before all of the
		// logic is finished running.
		if errors.Is(re.err, context.DeadlineExceeded) {
			re.SetStatus(http.StatusGatewayTimeout)
			re.SetMessage("The server could not process this request in time, please try again.")
		} else if strings.Contains(re.Cause().Error(), "context canceled") {
			re.SetStatus(http.StatusBadRequest)
			re.SetMessage("Request aborted by client.")
		}
	}

	// c.Writer.Status() will be a non-200 value if the headers have already been sent
	// to the requester but an error is encountered. This can happen if there is an issue
	// marshaling a struct placed into a c.JSON() call (or c.AbortWithJSON() call).
	if status >= 500 || c.Writer.Status() != 200 {
		event.WithField("status", status).WithField("error", re.err).Error("error while handling HTTP request")
	} else {
		event.WithField("status", status).WithField("error", re.err).Debug("error handling HTTP request (not a server error)")
	}
	if re.msg == "" {
		re.msg = "An unexpected error was encountered while processing this request"
	}
	// Now abort the request with the error message and include the unique request
	// ID that was present to make things super easy on people who don't know how
	// or cannot view the response headers (where X-Request-Id would be present).
	c.AbortWithStatusJSON(status, gin.H{"error": re.msg, "request_id": reqId})
}

// Cause returns the underlying error.
func (re *RequestError) Cause() error {
	return re.err
}

// Error returns the underlying error message for this request.
func (re *RequestError) Error() string {
	return re.err.Error()
}

// Looks at the given RequestError and determines if it is a specific filesystem
// error that we can process and return differently for the user.
//
// Some external things end up calling fmt.Errorf() on our filesystem errors
// which ends up just unleashing chaos on the system. For the sake of this,
// fallback to using text checks.
//
// If the error passed into this call is nil or does not match empty values will
// be returned to the caller.
func (re *RequestError) asFilesystemError() (int, string) {
	err := re.Cause()
	if err == nil {
		return 0, ""
	}
	if filesystem.IsErrorCode(err, filesystem.ErrNotExist) ||
		filesystem.IsErrorCode(err, filesystem.ErrCodePathResolution) ||
		strings.Contains(err.Error(), "resolves to a location outside the server root") {
		return http.StatusNotFound, "The requested resources was not found on the system."
	}
	if filesystem.IsErrorCode(err, filesystem.ErrCodeDenylistFile) || strings.Contains(err.Error(), "filesystem: file access prohibited") {
		return http.StatusForbidden, "This file cannot be modified: present in egg denylist."
	}
	if filesystem.IsErrorCode(err, filesystem.ErrCodeIsDirectory) || strings.Contains(err.Error(), "filesystem: is a directory") {
		return http.StatusBadRequest, "Cannot perform that action: file is a directory."
	}
	if filesystem.IsErrorCode(err, filesystem.ErrCodeDiskSpace) || strings.Contains(err.Error(), "filesystem: not enough disk space") {
		return http.StatusBadRequest, "There is not enough disk space available to perform that action."
	}
	if strings.HasSuffix(err.Error(), "file name too long") {
		return http.StatusBadRequest, "Cannot perform that action: file name is too long."
	}
	if e, ok := err.(*os.SyscallError); ok && e.Syscall == "readdirent" {
		return http.StatusNotFound, "The requested directory does not exist."
	}
	return 0, ""
}
