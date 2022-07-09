package server

import (
	"emperror.dev/errors"
	"github.com/apex/log"
	"github.com/goccy/go-json"
	"github.com/pterodactyl/wings/internal/database"
	"github.com/xujiajun/nutsdb"
	"regexp"
	"time"
)

type Event string
type ActivityMeta map[string]interface{}

const ActivityPowerPrefix = "power_"

const (
	ActivityConsoleCommand = Event("console_command")
)

var ipTrimRegex = regexp.MustCompile(`(:\d*)?$`)

type Activity struct {
	// User is UUID of the user that triggered this event, or an empty string if the event
	// cannot be tied to a specific user, in which case we will assume it was the system
	// user.
	User string `json:"user"`
	// Server is the UUID of the server this event is associated with.
	Server string `json:"server"`
	// Event is a string that describes what occurred, and is used by the Panel instance to
	// properly associate this event in the activity logs.
	Event Event `json:"event"`
	// Metadata is either a null value, string, or a JSON blob with additional event specific
	// metadata that can be provided.
	Metadata ActivityMeta `json:"metadata"`
	// IP is the IP address that triggered this event, or an empty string if it cannot be
	// determined properly.
	IP        string    `json:"ip"`
	Timestamp time.Time `json:"timestamp"`
}

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
func (ra RequestActivity) Event(event Event, metadata ActivityMeta) Activity {
	return Activity{
		User:     ra.user,
		Server:   ra.server,
		IP:       ra.ip,
		Event:    event,
		Metadata: metadata,
	}
}

// Save creates a new event instance and saves it. If an error is encountered it is automatically
// logged to the provided server's error logging output. The error is also returned to the caller
// but can be ignored.
func (ra RequestActivity) Save(s *Server, event Event, metadata ActivityMeta) error {
	if err := ra.Event(event, metadata).Save(); err != nil {
		s.Log().WithField("error", err).WithField("event", event).Error("activity: failed to save event")
		return errors.WithStack(err)
	}
	return nil
}

// IP returns the IP address associated with this entry.
func (ra RequestActivity) IP() string {
	return ra.ip
}

// SetUser clones the RequestActivity struct and sets a new user value on the copy
// before returning it.
func (ra RequestActivity) SetUser(u string) RequestActivity {
	c := ra
	c.user = u
	return c
}

// Save logs the provided event using Wings' internal K/V store so that we can then
// pass it along to the Panel at set intervals. In addition, this will ensure that the events
// are persisted to the disk, even between instance restarts.
func (a Activity) Save() error {
	if a.Timestamp.IsZero() {
		a.Timestamp = time.Now().UTC()
	}

	// Since the "RemoteAddr" field can often include a port on the end we need to
	// trim that off, otherwise it'll fail validation when sent to the Panel.
	a.IP = ipTrimRegex.ReplaceAllString(a.IP, "")

	value, err := json.Marshal(a)
	if err != nil {
		return errors.Wrap(err, "database: failed to marshal activity into json bytes")
	}

	return database.DB().Update(func(tx *nutsdb.Tx) error {
		log.WithField("subsystem", "activity").
			WithFields(log.Fields{"server": a.Server, "user": a.User, "event": a.Event, "ip": a.IP}).
			Debug("saving activity to database")

		if err := tx.RPush(database.ServerActivityBucket, []byte("events"), value); err != nil {
			return errors.WithStack(err)
		}
		return nil
	})
}

func (s *Server) NewRequestActivity(user string, ip string) RequestActivity {
	return RequestActivity{server: s.ID(), user: user, ip: ip}
}

// NewActivity creates a new event instance for the server in question.
func (s *Server) NewActivity(user string, event Event, metadata ActivityMeta, ip string) Activity {
	return Activity{
		User:     user,
		Server:   s.ID(),
		Event:    event,
		Metadata: metadata,
		IP:       ip,
	}
}
