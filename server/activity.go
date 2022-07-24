package server

import (
	"emperror.dev/errors"
	"github.com/pterodactyl/wings/internal/database"
	"github.com/pterodactyl/wings/internal/models"
)

const ActivityPowerPrefix = "server:power."

const (
	ActivityConsoleCommand      = models.Event("server:console.command")
	ActivitySftpWrite           = models.Event("server:sftp.write")
	ActivitySftpCreate          = models.Event("server:sftp.create")
	ActivitySftpCreateDirectory = models.Event("server:sftp.create-directory")
	ActivitySftpRename          = models.Event("server:sftp.rename")
	ActivitySftpDelete          = models.Event("server:sftp.delete")
)

// RequestActivity is a wrapper around a LoggedEvent that is able to track additional request
// specific metadata including the specific user and IP address associated with all subsequent
// events. The internal logged event structure can be extracted by calling RequestEvent.Event().
type RequestActivity struct {
	server string
	user   string
	ip     string
}

// Event returns the underlying logged event from the RequestEvent instance and sets the
// specific event and metadata on it.
func (ra RequestActivity) Event(event models.Event, metadata models.ActivityMeta) *models.Activity {
	a := models.Activity{Server: ra.server, IP: ra.ip, Event: event, Metadata: metadata}

	return a.SetUser(ra.user)
}

// Save creates a new event instance and saves it. If an error is encountered it is automatically
// logged to the provided server's error logging output. The error is also returned to the caller
// but can be ignored.
func (ra RequestActivity) Save(s *Server, event models.Event, metadata models.ActivityMeta) error {
	if tx := database.Instance().Create(ra.Event(event, metadata)); tx.Error != nil {
		err := errors.WithStackIf(tx.Error)

		s.Log().WithField("error", err).WithField("event", event).Error("activity: failed to save event")

		return err
	}
	return nil
}

// IP returns the IP address associated with this entry.
func (ra RequestActivity) IP() string {
	return ra.ip
}

func (ra *RequestActivity) User() string {
	return ra.user
}

// SetUser clones the RequestActivity struct and sets a new user value on the copy
// before returning it.
func (ra RequestActivity) SetUser(u string) RequestActivity {
	c := ra
	c.user = u
	return c
}

func (s *Server) NewRequestActivity(user string, ip string) RequestActivity {
	return RequestActivity{server: s.ID(), user: user, ip: ip}
}
