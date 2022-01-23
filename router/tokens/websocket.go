package tokens

import (
	"strings"
	"sync"
	"time"

	"github.com/apex/log"
	"github.com/gbrlsnchs/jwt/v3"
	"github.com/goccy/go-json"
)

// The time at which Wings was booted. No JWT's created before this time are allowed to
// connect to the socket since they may have been marked as denied already and therefore
// could be invalid at this point.
//
// By doing this we make it so that a user who gets disconnected from Wings due to a Wings
// reboot just needs to request a new token as if their old token had expired naturally.
var wingsBootTime = time.Now()

// A map that contains any JTI's that have been denied by the Panel and the time at which
// they were marked as denied. Therefore any JWT with the same JTI and an IssuedTime that
// is the same as or before this time should be considered invalid.
//
// This is used to allow the Panel to revoke tokens en-masse for a given user & server
// combination since the JTI for tokens is just MD5(user.id + server.uuid). When a server
// is booted this listing is fetched from the panel and the Websocket is dynamically updated.
var denylist sync.Map

// Adds a JTI to the denylist by marking any JWTs generated before the current time as
// being invalid if they use the same JTI.
func DenyJTI(jti string) {
	log.WithField("jti", jti).Debugf("adding \"%s\" to JTI denylist", jti)

	denylist.Store(jti, time.Now())
}

// A JWT payload for Websocket connections. This JWT is passed along to the Websocket after
// it has been connected to by sending an "auth" event.
type WebsocketPayload struct {
	jwt.Payload
	sync.RWMutex

	UserID      json.Number `json:"user_id"`
	ServerUUID  string      `json:"server_uuid"`
	Permissions []string    `json:"permissions"`
}

// Returns the JWT payload.
func (p *WebsocketPayload) GetPayload() *jwt.Payload {
	p.RLock()
	defer p.RUnlock()

	return &p.Payload
}

// Returns the UUID of the server associated with this JWT.
func (p *WebsocketPayload) GetServerUuid() string {
	p.RLock()
	defer p.RUnlock()

	return p.ServerUUID
}

// Check if the JWT has been marked as denied by the instance due to either being issued
// before Wings was booted, or because we have denied all tokens with the same JTI
// occurring before a set time.
func (p *WebsocketPayload) Denylisted() bool {
	// If there is no IssuedAt present for the token, we cannot validate the token so
	// just immediately mark it as not valid.
	if p.IssuedAt == nil {
		return true
	}

	// If the time that the token was issued is before the time at which Wings was booted
	// then the token is invalid for our purposes, even if the token "has permission".
	if p.IssuedAt.Time.Before(wingsBootTime) {
		return true
	}

	// Finally, if the token was issued before a time that is currently denied for this
	// token instance, ignore the permissions response.
	if t, ok := denylist.Load(p.JWTID); ok {
		if p.IssuedAt.Time.Before(t.(time.Time)) {
			return true
		}
	}

	return false
}

// Checks if the given token payload has a permission string.
func (p *WebsocketPayload) HasPermission(permission string) bool {
	p.RLock()
	defer p.RUnlock()

	for _, k := range p.Permissions {
		if k == permission || (!strings.HasPrefix(permission, "admin") && k == "*") {
			return !p.Denylisted()
		}
	}

	return false
}
