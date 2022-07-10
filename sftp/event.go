package sftp

import (
	"emperror.dev/errors"
	"github.com/apex/log"
	"github.com/pterodactyl/wings/server"
	"time"
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
func (eh *eventHandler) Log(e server.Event, fa FileAction) error {
	metadata := map[string]interface{}{
		"files": []string{fa.Entity},
	}
	if fa.Target != "" {
		metadata["files"] = []map[string]string{
			{"from": fa.Entity, "to": fa.Target},
		}
	}

	r := server.Activity{
		User:      eh.user,
		Server:    eh.server,
		Event:     e,
		Metadata:  metadata,
		IP:        eh.ip,
		Timestamp: time.Now().UTC(),
	}

	return errors.Wrap(r.Save(), "sftp: failed to store file event")
}

// MustLog is a wrapper around log that will trigger a fatal error and exit the application
// if an error is encountered during the logging of the event.
func (eh *eventHandler) MustLog(e server.Event, fa FileAction) {
	if err := eh.Log(e, fa); err != nil {
		log.WithField("error", err).Fatal("sftp: failed to log event")
	}
}
