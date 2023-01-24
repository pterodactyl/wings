package models

import (
	"net"
	"strings"
	"time"

	"gorm.io/gorm"
)

type Event string

type ActivityMeta map[string]interface{}

// Activity defines an activity log event for a server entity performed by a user. This is
// used for tracking commands, power actions, and SFTP events so that they can be reconciled
// and sent back to the Panel instance to be displayed to the user.
type Activity struct {
	ID int `gorm:"primaryKey;not null" json:"-"`
	// User is UUID of the user that triggered this event, or an empty string if the event
	// cannot be tied to a specific user, in which case we will assume it was the system
	// user.
	User JsonNullString `gorm:"type:uuid" json:"user"`
	// Server is the UUID of the server this event is associated with.
	Server string `gorm:"type:uuid;not null" json:"server"`
	// Event is a string that describes what occurred, and is used by the Panel instance to
	// properly associate this event in the activity logs.
	Event Event `gorm:"index;not null" json:"event"`
	// Metadata is either a null value, string, or a JSON blob with additional event specific
	// metadata that can be provided.
	Metadata ActivityMeta `gorm:"serializer:json" json:"metadata"`
	// IP is the IP address that triggered this event, or an empty string if it cannot be
	// determined properly. This should be the connecting user's IP address, and not the
	// internal system IP.
	IP        string    `gorm:"not null" json:"ip"`
	Timestamp time.Time `gorm:"not null" json:"timestamp"`
}

// SetUser sets the current user that performed the action. If an empty string is provided
// it is cast into a null value when stored.
func (a Activity) SetUser(u string) *Activity {
	var ns JsonNullString
	if u == "" {
		if err := ns.Scan(nil); err != nil {
			panic(err)
		}
	} else {
		if err := ns.Scan(u); err != nil {
			panic(err)
		}
	}
	a.User = ns
	return &a
}

// BeforeCreate executes before we create any activity entry to ensure the IP address
// is trimmed down to remove any extraneous data, and the timestamp is set to the current
// system time and then stored as UTC.
func (a *Activity) BeforeCreate(_ *gorm.DB) error {
	if ip, _, err := net.SplitHostPort(strings.TrimSpace(a.IP)); err == nil {
		a.IP = ip
	}
	if a.Timestamp.IsZero() {
		a.Timestamp = time.Now()
	}
	a.Timestamp = a.Timestamp.UTC()
	if a.Metadata == nil {
		a.Metadata = ActivityMeta{}
	}
	return nil
}
