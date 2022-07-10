package sftp

import (
	"bytes"
	"emperror.dev/errors"
	"encoding/gob"
	"github.com/apex/log"
	"github.com/pterodactyl/wings/internal/database"
	"github.com/xujiajun/nutsdb"
	"regexp"
	"time"
)

type eventHandler struct {
	ip     string
	user   string
	server string
}

type Event string
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

type EventRecord struct {
	Event     Event
	Action    FileAction
	IP        string
	User      string
	Timestamp time.Time
}

const (
	EventWrite           = Event("write")
	EventCreate          = Event("create")
	EventCreateDirectory = Event("create-directory")
	EventRename          = Event("rename")
	EventDelete          = Event("delete")
)

var ipTrimRegex = regexp.MustCompile(`(:\d*)?$`)

// Log logs an event into the Wings bucket for SFTP activity which then allows a seperate
// cron to run and parse the events into a more manageable stream of event data to send
// back to the Panel instance.
func (eh *eventHandler) Log(e Event, fa FileAction) error {
	r := EventRecord{
		Event:     e,
		Action:    fa,
		IP:        ipTrimRegex.ReplaceAllString(eh.ip, ""),
		User:      eh.user,
		Timestamp: time.Now().UTC(),
	}

	var buf bytes.Buffer
	enc := gob.NewEncoder(&buf)
	if err := enc.Encode(r); err != nil {
		return errors.Wrap(err, "sftp: failed to encode event")
	}

	return database.DB().Update(func(tx *nutsdb.Tx) error {
		if err := tx.RPush(database.SftpActivityBucket, []byte(eh.server), buf.Bytes()); err != nil {
			return errors.Wrap(err, "sftp: failed to push event to stack")
		}
		return nil
	})
}

// MustLog is a wrapper around log that will trigger a fatal error and exit the application
// if an error is encountered during the logging of the event.
func (eh *eventHandler) MustLog(e Event, fa FileAction) {
	if err := eh.Log(e, fa); err != nil {
		log.WithField("error", err).Fatal("sftp: failed to log event")
	}
}
