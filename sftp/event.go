package sftp

import (
	"emperror.dev/errors"
	"github.com/apex/log"

	"github.com/pterodactyl/wings/internal/database"
	"github.com/pterodactyl/wings/internal/models"
)

type eventHandler struct {
	ip     string
	user   string
	server string
}

type FileAction struct {
	// Entity is the targeted file or directory (depending on the event) that the action
	// is being performed _against_, such as "/foo/test.txt". This will always be the full
	// path to the element.
	Entity string
	// Target is an optional (often blank) field that only has a value in it when the event
	// is specifically modifying the entity, such as a rename or move event. In that case
	// the Target field will be the final value, such as "/bar/new.txt"
	Target string
}

// Log parses a SFTP specific file activity event and then passes it off to be stored
// in the normal activity database.
func (eh *eventHandler) Log(e models.Event, fa FileAction) error {
	metadata := map[string]interface{}{
		"files": []string{fa.Entity},
	}
	if fa.Target != "" {
		metadata["files"] = []map[string]string{
			{"from": fa.Entity, "to": fa.Target},
		}
	}

	a := models.Activity{
		Server:   eh.server,
		Event:    e,
		Metadata: metadata,
		IP:       eh.ip,
	}

	if tx := database.Instance().Create(a.SetUser(eh.user)); tx.Error != nil {
		return errors.WithStack(tx.Error)
	}
	return nil
}

// MustLog is a wrapper around log that will trigger a fatal error and exit the application
// if an error is encountered during the logging of the event.
func (eh *eventHandler) MustLog(e models.Event, fa FileAction) {
	if err := eh.Log(e, fa); err != nil {
		log.WithField("error", errors.WithStack(err)).WithField("event", e).Error("sftp: failed to log event")
	}
}
