package websocket

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/pterodactyl/wings/internal/models"

	"emperror.dev/errors"
	"github.com/apex/log"
	"github.com/gbrlsnchs/jwt/v3"
	"github.com/gin-gonic/gin"
	"github.com/goccy/go-json"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"

	"github.com/pterodactyl/wings/system"

	"github.com/pterodactyl/wings/config"
	"github.com/pterodactyl/wings/environment"
	"github.com/pterodactyl/wings/environment/docker"
	"github.com/pterodactyl/wings/router/tokens"
	"github.com/pterodactyl/wings/server"
)

const (
	PermissionConnect          = "websocket.connect"
	PermissionSendCommand      = "control.console"
	PermissionSendPowerStart   = "control.start"
	PermissionSendPowerStop    = "control.stop"
	PermissionSendPowerRestart = "control.restart"
	PermissionReceiveErrors    = "admin.websocket.errors"
	PermissionReceiveInstall   = "admin.websocket.install"
	PermissionReceiveTransfer  = "admin.websocket.transfer"
	PermissionReceiveBackups   = "backup.read"
)

type Handler struct {
	sync.RWMutex `json:"-"`
	Connection   *websocket.Conn `json:"-"`
	jwt          *tokens.WebsocketPayload
	server       *server.Server
	ra           server.RequestActivity
	uuid         uuid.UUID
}

var (
	ErrJwtNotPresent    = errors.New("jwt: no jwt present")
	ErrJwtNoConnectPerm = errors.New("jwt: missing connect permission")
	ErrJwtUuidMismatch  = errors.New("jwt: server uuid mismatch")
	ErrJwtOnDenylist    = errors.New("jwt: created too far in past (denylist)")
)

func IsJwtError(err error) bool {
	return errors.Is(err, ErrJwtNotPresent) ||
		errors.Is(err, ErrJwtNoConnectPerm) ||
		errors.Is(err, ErrJwtUuidMismatch) ||
		errors.Is(err, ErrJwtOnDenylist) ||
		errors.Is(err, jwt.ErrExpValidation)
}

// NewTokenPayload parses a JWT into a websocket token payload.
func NewTokenPayload(token []byte) (*tokens.WebsocketPayload, error) {
	var payload tokens.WebsocketPayload
	if err := tokens.ParseToken(token, &payload); err != nil {
		return nil, err
	}

	if payload.Denylisted() {
		return nil, ErrJwtOnDenylist
	}

	if !payload.HasPermission(PermissionConnect) {
		return nil, ErrJwtNoConnectPerm
	}

	return &payload, nil
}

// GetHandler returns a new websocket handler using the context provided.
func GetHandler(s *server.Server, w http.ResponseWriter, r *http.Request, c *gin.Context) (*Handler, error) {
	upgrader := websocket.Upgrader{
		// Ensure that the websocket request is originating from the Panel itself,
		// and not some other location.
		CheckOrigin: func(r *http.Request) bool {
			o := r.Header.Get("Origin")
			if o == config.Get().PanelLocation {
				return true
			}
			for _, origin := range config.Get().AllowedOrigins {
				if origin == "*" || origin == o {
					return true
				}
			}
			return false
		},
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return nil, err
	}

	u, err := uuid.NewRandom()
	if err != nil {
		return nil, err
	}

	return &Handler{
		Connection: conn,
		jwt:        nil,
		server:     s,
		ra:         s.NewRequestActivity("", c.ClientIP()),
		uuid:       u,
	}, nil
}

func (h *Handler) Uuid() uuid.UUID {
	return h.uuid
}

func (h *Handler) Logger() *log.Entry {
	return log.WithField("subsystem", "websocket").
		WithField("connection", h.Uuid().String()).
		WithField("server", h.server.ID())
}

func (h *Handler) SendJson(v Message) error {
	// Do not send JSON down the line if the JWT on the connection is not valid!
	if err := h.TokenValid(); err != nil {
		_ = h.unsafeSendJson(Message{
			Event: JwtErrorEvent,
			Args:  []string{err.Error()},
		})
		return nil
	}

	if j := h.GetJwt(); j != nil {
		// If we're sending installation output but the user does not have the required
		// permissions to see the output, don't send it down the line.
		if v.Event == server.InstallOutputEvent {
			if !j.HasPermission(PermissionReceiveInstall) {
				return nil
			}
		}

		// If the user does not have permission to see backup events, do not emit
		// them over the socket.
		if strings.HasPrefix(v.Event, server.BackupCompletedEvent) {
			if !j.HasPermission(PermissionReceiveBackups) {
				return nil
			}
		}

		// If we are sending transfer output, only send it to the user if they have the required permissions.
		if v.Event == server.TransferLogsEvent {
			if !j.HasPermission(PermissionReceiveTransfer) {
				return nil
			}
		}
	}

	if err := h.unsafeSendJson(v); err != nil {
		// Not entirely sure how this happens (likely just when there is a ton of console spam)
		// but I don't care to fix it right now, so just mask the error and throw a warning into
		// the logs for us to look into later.
		if errors.Is(err, websocket.ErrCloseSent) {
			if h.server != nil {
				h.server.Log().WithField("subsystem", "websocket").
					WithField("event", v.Event).
					Warn("failed to send event to websocket: close already sent")
			}
			return nil
		}

		return err
	}

	return nil
}

// Sends JSON over the websocket connection, ignoring the authentication state of the
// socket user. Do not call this directly unless you are positive a response should be
// sent back to the client!
func (h *Handler) unsafeSendJson(v interface{}) error {
	h.Lock()
	defer h.Unlock()

	return h.Connection.WriteJSON(v)
}

// TokenValid checks if the JWT is still valid.
func (h *Handler) TokenValid() error {
	j := h.GetJwt()
	if j == nil {
		return ErrJwtNotPresent
	}

	if err := jwt.ExpirationTimeValidator(time.Now())(&j.Payload); err != nil {
		return err
	}

	if j.Denylisted() {
		return ErrJwtOnDenylist
	}

	if !j.HasPermission(PermissionConnect) {
		return ErrJwtNoConnectPerm
	}

	if h.server.ID() != j.GetServerUuid() {
		return ErrJwtUuidMismatch
	}

	return nil
}

// SendErrorJson sends an error back to the connected websocket instance by checking the permissions
// of the token. If the user has the "receive-errors" grant we will send back the actual
// error message, otherwise we just send back a standard error message.
func (h *Handler) SendErrorJson(msg Message, err error, shouldLog ...bool) error {
	j := h.GetJwt()
	isJWTError := IsJwtError(err)

	wsm := Message{
		Event: ErrorEvent,
		Args:  []string{"an unexpected error was encountered while handling this request"},
	}

	if isJWTError || (j != nil && j.HasPermission(PermissionReceiveErrors)) {
		if isJWTError {
			wsm.Event = JwtErrorEvent
		}
		wsm.Args = []string{err.Error()}
	}

	m, u := h.GetErrorMessage(wsm.Args[0])
	wsm.Args = []string{m}

	if !isJWTError && (len(shouldLog) == 0 || (len(shouldLog) == 1 && shouldLog[0] == true)) {
		h.server.Log().WithFields(log.Fields{"event": msg.Event, "error_identifier": u.String(), "error": err}).
			Errorf("error processing websocket event \"%s\"", msg.Event)
	}

	return h.unsafeSendJson(wsm)
}

// GetErrorMessage converts an error message into a more readable representation and returns a UUID
// that can be cross-referenced to find the specific error that triggered.
func (h *Handler) GetErrorMessage(msg string) (string, uuid.UUID) {
	u := uuid.Must(uuid.NewRandom())

	m := fmt.Sprintf("Error Event [%s]: %s", u.String(), msg)

	return m, u
}

// GetJwt returns the JWT for the websocket in a race-safe manner.
func (h *Handler) GetJwt() *tokens.WebsocketPayload {
	h.RLock()
	defer h.RUnlock()

	return h.jwt
}

// setJwt sets the JWT for the websocket in a race-safe manner.
func (h *Handler) setJwt(token *tokens.WebsocketPayload) {
	h.Lock()
	h.ra = h.ra.SetUser(token.UserUUID)
	h.jwt = token
	h.Unlock()
}

// HandleInbound handles an inbound socket request and route it to the proper action.
func (h *Handler) HandleInbound(ctx context.Context, m Message) error {
	if m.Event != AuthenticationEvent {
		if err := h.TokenValid(); err != nil {
			h.unsafeSendJson(Message{
				Event: JwtErrorEvent,
				Args:  []string{err.Error()},
			})
			return nil
		}
	}

	switch m.Event {
	case AuthenticationEvent:
		{
			token, err := NewTokenPayload([]byte(strings.Join(m.Args, "")))
			if err != nil {
				return err
			}

			// Check if the user has previously authenticated successfully.
			newConnection := h.GetJwt() == nil

			// Previously there was a HasPermission(PermissionConnect) check around this,
			// however NewTokenPayload will return an error if it doesn't have the connect
			// permission meaning that it was a redundant function call.
			h.setJwt(token)

			// Tell the client they authenticated successfully.
			_ = h.unsafeSendJson(Message{Event: AuthenticationSuccessEvent})

			// Check if the client was refreshing their authentication token
			// instead of authenticating for the first time.
			if !newConnection {
				// This prevents duplicate status messages as outlined in
				// https://github.com/pterodactyl/panel/issues/2077
				return nil
			}

			// Now that we've authenticated with the token and confirmed that we're not
			// reconnecting to the socket, register the event listeners for the server and
			// the token expiration.
			h.registerListenerEvents(ctx)

			// On every authentication event, send the current server status back
			// to the client. :)
			state := h.server.Environment.State()
			_ = h.SendJson(Message{
				Event: server.StatusEvent,
				Args:  []string{state},
			})

			// Only send the current disk usage if the server is offline, if docker container is running,
			// Environment#EnableResourcePolling() will send this data to all clients.
			if state == environment.ProcessOfflineState {
				if !h.server.IsInstalling() && !h.server.IsTransferring() {
					_ = h.server.Filesystem().HasSpaceAvailable(false)

					b, _ := json.Marshal(h.server.Proc())
					_ = h.SendJson(Message{
						Event: server.StatsEvent,
						Args:  []string{string(b)},
					})
				}
			}

			return nil
		}
	case SetStateEvent:
		{
			action := server.PowerAction(strings.Join(m.Args, ""))

			actions := make(map[server.PowerAction]string)
			actions[server.PowerActionStart] = PermissionSendPowerStart
			actions[server.PowerActionStop] = PermissionSendPowerStop
			actions[server.PowerActionRestart] = PermissionSendPowerRestart
			actions[server.PowerActionTerminate] = PermissionSendPowerStop

			// Check that they have permission to perform this action if it is needed.
			if permission, exists := actions[action]; exists {
				if !h.GetJwt().HasPermission(permission) {
					return nil
				}
			}

			err := h.server.HandlePowerAction(action)
			if errors.Is(err, system.ErrLockerLocked) {
				m, _ := h.GetErrorMessage("another power action is currently being processed for this server, please try again later")

				_ = h.SendJson(Message{
					Event: ErrorEvent,
					Args:  []string{m},
				})

				return nil
			}

			if err == nil {
				h.server.SaveActivity(h.ra, models.Event(server.ActivityPowerPrefix+action), nil)
			}

			return err
		}
	case SendServerLogsEvent:
		{
			ctx, cancel := context.WithTimeout(context.Background(), time.Second*5)
			defer cancel()
			if running, _ := h.server.Environment.IsRunning(ctx); !running {
				return nil
			}

			logs, err := h.server.Environment.Readlog(config.Get().System.WebsocketLogCount)
			if err != nil {
				return err
			}

			for _, line := range logs {
				_ = h.SendJson(Message{
					Event: server.ConsoleOutputEvent,
					Args:  []string{line},
				})
			}

			return nil
		}
	case SendStatsEvent:
		{
			b, _ := json.Marshal(h.server.Proc())
			_ = h.SendJson(Message{
				Event: server.StatsEvent,
				Args:  []string{string(b)},
			})

			return nil
		}
	case SendCommandEvent:
		{
			if !h.GetJwt().HasPermission(PermissionSendCommand) {
				return nil
			}

			if h.server.Environment.State() == environment.ProcessOfflineState {
				return nil
			}

			// TODO(dane): should probably add a new process state that is "booting environment" or something
			//  so that we can better handle this and only set the environment to booted once we're attached.
			//
			//  Or maybe just an IsBooted function?
			if h.server.Environment.State() == environment.ProcessStartingState {
				if e, ok := h.server.Environment.(*docker.Environment); ok {
					if !e.IsAttached() {
						return nil
					}
				}
			}

			if err := h.server.Environment.SendCommand(strings.Join(m.Args, "")); err != nil {
				return err
			}
			h.server.SaveActivity(h.ra, server.ActivityConsoleCommand, models.ActivityMeta{
				"command": strings.Join(m.Args, ""),
			})
			return nil
		}
	}

	return nil
}
