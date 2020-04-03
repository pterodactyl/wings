package main

import (
	"encoding/json"
	"fmt"
	"github.com/gbrlsnchs/jwt/v3"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/julienschmidt/httprouter"
	"github.com/pkg/errors"
	"github.com/pterodactyl/wings/config"
	"github.com/pterodactyl/wings/server"
	"go.uber.org/zap"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

const (
	AuthenticationSuccessEvent = "auth success"
	TokenExpiringEvent         = "token expiring"
	TokenExpiredEvent          = "token expired"
	AuthenticationEvent        = "auth"
	SetStateEvent              = "set state"
	SendServerLogsEvent        = "send logs"
	SendCommandEvent           = "send command"
	ErrorEvent                 = "daemon error"
)

type WebsocketMessage struct {
	// The event to perform. Should be one of the following that are supported:
	//
	// - status : Returns the server's power state.
	// - logs : Returns the server log data at the time of the request.
	// - power : Performs a power action aganist the server based the data.
	// - command : Performs a command on a server using the data field.
	Event string `json:"event"`

	// The data to pass along, only used by power/command currently. Other requests
	// should either omit the field or pass an empty value as it is ignored.
	Args []string `json:"args,omitempty"`

	// Is set to true when the request is originating from outside of the Daemon,
	// otherwise set to false for outbound.
	inbound bool
}

type WebsocketHandler struct {
	Server     *server.Server
	Mutex      sync.Mutex
	Connection *websocket.Conn
	JWT        *WebsocketTokenPayload
}

type WebsocketTokenPayload struct {
	jwt.Payload
	UserID      json.Number `json:"user_id"`
	ServerUUID  string      `json:"server_uuid"`
	Permissions []string    `json:"permissions"`
}

const (
	PermissionConnect        = "connect"
	PermissionSendCommand    = "send-command"
	PermissionSendPower      = "send-power"
	PermissionReceiveErrors  = "receive-errors"
	PermissionReceiveInstall = "receive-install"
)

// Checks if the given token payload has a permission string.
func (wtp *WebsocketTokenPayload) HasPermission(permission string) bool {
	for _, k := range wtp.Permissions {
		if k == permission {
			return true
		}
	}

	return false
}

var alg *jwt.HMACSHA

// Validates the provided JWT against the known secret for the Daemon and returns the
// parsed data.
//
// This function DOES NOT validate that the token is valid for the connected server, nor
// does it ensure that the user providing the token is able to actually do things.
func ParseJWT(token []byte) (*WebsocketTokenPayload, error) {
	var payload WebsocketTokenPayload
	if alg == nil {
		alg = jwt.NewHS256([]byte(config.Get().AuthenticationToken))
	}

	now := time.Now()
	verifyOptions := jwt.ValidatePayload(
		&payload.Payload,
		jwt.ExpirationTimeValidator(now),
	)

	_, err := jwt.Verify(token, alg, &payload, verifyOptions)
	if err != nil {
		return nil, err
	}

	if !payload.HasPermission(PermissionConnect) {
		return nil, errors.New("not authorized to connect to this socket")
	}

	return &payload, nil
}

// Checks if the JWT is still valid.
func (wsh *WebsocketHandler) TokenValid() error {
	if wsh.JWT == nil {
		return errors.New("no jwt present")
	}

	if err := jwt.ExpirationTimeValidator(time.Now())(&wsh.JWT.Payload); err != nil {
		return err
	}

	if !wsh.JWT.HasPermission(PermissionConnect) {
		return errors.New("jwt does not have connect permission")
	}

	if wsh.Server.Uuid != wsh.JWT.ServerUUID {
		return errors.New("jwt server uuid mismatch")
	}

	return nil
}

// Handle a request for a specific server websocket. This will handle inbound requests as well
// as ensure that any console output is also passed down the wire on the socket.
func (rt *Router) routeWebsocket(w http.ResponseWriter, r *http.Request, ps httprouter.Params) {
	c, err := rt.upgrader.Upgrade(w, r, nil)
	if err != nil {
		zap.S().Errorw("error upgrading websocket", zap.Error(errors.WithStack(err)))
		http.Error(w, "failed to upgrade websocket", http.StatusInternalServerError)

		return
	}

	// Make a ticker and completion channel that is used to continuously poll the
	// JWT stored in the session to send events to the socket when it is expiring.
	ticker := time.NewTicker(time.Second * 30)
	done := make(chan bool)

	// Whenever this function is complete, end the ticker, close out the channel,
	// and then close the websocket connection.
	defer func() {
		ticker.Stop()
		done <- true
		c.Close()
	}()

	s := rt.GetServer(ps.ByName("server"))
	handler := WebsocketHandler{
		Server:     s,
		Mutex:      sync.Mutex{},
		Connection: c,
		JWT:        nil,
	}

	events := []string{
		server.StatsEvent,
		server.StatusEvent,
		server.ConsoleOutputEvent,
		server.InstallOutputEvent,
		server.DaemonMessageEvent,
	}

	eventChannel := make(chan server.Event)
	for _, event := range events {
		s.Events().Subscribe(event, eventChannel)
	}

	defer func() {
		for _, event := range events {
			s.Events().Unsubscribe(event, eventChannel)
		}

		close(eventChannel)
	}()

	// Listen for different events emitted by the server and respond to them appropriately.
	go func() {
		for d := range eventChannel {
			handler.SendJson(&WebsocketMessage{
				Event: d.Topic,
				Args:  []string{d.Data},
			})
		}
	}()
	// Sit here and check the time to expiration on the JWT every 30 seconds until
	// the token has expired. If we are within 3 minutes of the token expiring, send
	// a notice over the socket that it is expiring soon. If it has expired, send that
	// notice as well.
	go func() {
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				{
					if handler.JWT != nil {
						if handler.JWT.ExpirationTime.Unix()-time.Now().Unix() <= 0 {
							handler.SendJson(&WebsocketMessage{Event: TokenExpiredEvent})
						} else if handler.JWT.ExpirationTime.Unix()-time.Now().Unix() <= 180 {
							handler.SendJson(&WebsocketMessage{Event: TokenExpiringEvent})
						}
					}
				}
			}
		}
	}()

	for {
		j := WebsocketMessage{inbound: true}

		_, p, err := c.ReadMessage()
		if err != nil {
			if !websocket.IsCloseError(
				err,
				websocket.CloseNormalClosure,
				websocket.CloseGoingAway,
				websocket.CloseNoStatusReceived,
				websocket.CloseServiceRestart,
				websocket.CloseAbnormalClosure,
			) {
				zap.S().Errorw("error handling websocket message", zap.Error(err))
			}
			break
		}

		// Discard and JSON parse errors into the void and don't continue processing this
		// specific socket request. If we did a break here the client would get disconnected
		// from the socket, which is NOT what we want to do.
		if err := json.Unmarshal(p, &j); err != nil {
			continue
		}

		if err := handler.HandleInbound(j); err != nil {
			handler.SendErrorJson(err)
		}
	}
}

// Perform a blocking send operation on the websocket since we want to avoid any
// concurrent writes to the connection, which would cause a runtime panic and cause
// the program to crash out.
func (wsh *WebsocketHandler) SendJson(v *WebsocketMessage) error {
	// Do not send JSON down the line if the JWT on the connection is not
	// valid!
	if err := wsh.TokenValid(); err != nil {
		return nil
	}

	// If we're sending installation output but the user does not have the required
	// permissions to see the output, don't send it down the line.
	if v.Event == server.InstallOutputEvent {
		zap.S().Debugf("%+v", v.Args)
		if wsh.JWT != nil && !wsh.JWT.HasPermission(PermissionReceiveInstall) {
			return nil
		}
	}

	return wsh.unsafeSendJson(v)
}

// Sends JSON over the websocket connection, ignoring the authentication state of the
// socket user. Do not call this directly unless you are positive a response should be
// sent back to the client!
func (wsh *WebsocketHandler) unsafeSendJson(v interface{}) error {
	wsh.Mutex.Lock()
	defer wsh.Mutex.Unlock()

	return wsh.Connection.WriteJSON(v)
}

// Sends an error back to the connected websocket instance by checking the permissions
// of the token. If the user has the "receive-errors" grant we will send back the actual
// error message, otherwise we just send back a standard error message.
func (wsh *WebsocketHandler) SendErrorJson(err error) error {
	wsh.Mutex.Lock()
	defer wsh.Mutex.Unlock()

	message := "an unexpected error was encountered while handling this request"
	if wsh.JWT != nil {
		if server.IsSuspendedError(err) || wsh.JWT.HasPermission(PermissionReceiveErrors) {
			message = err.Error()
		}
	}

	m, u := wsh.GetErrorMessage(message)

	wsm := WebsocketMessage{Event: ErrorEvent}
	wsm.Args = []string{m}

	if !server.IsSuspendedError(err) {
		zap.S().Errorw(
			"an error was encountered in the websocket process",
			zap.String("server", wsh.Server.Uuid),
			zap.String("error_identifier", u.String()),
			zap.Error(err),
		)
	}

	return wsh.Connection.WriteJSON(wsm)
}

// Converts an error message into a more readable representation and returns a UUID
// that can be cross-referenced to find the specific error that triggered.
func (wsh *WebsocketHandler) GetErrorMessage(msg string) (string, uuid.UUID) {
	u, _ := uuid.NewRandom()

	m := fmt.Sprintf("Error Event [%s]: %s", u.String(), msg)

	return m, u
}

// Handle the inbound socket request and route it to the proper server action.
func (wsh *WebsocketHandler) HandleInbound(m WebsocketMessage) error {
	if !m.inbound {
		return errors.New("cannot handle websocket message, not an inbound connection")
	}

	if m.Event != AuthenticationEvent {
		if err := wsh.TokenValid(); err != nil {
			zap.S().Debugw("jwt token is no longer valid", zap.String("message", err.Error()))

			wsh.unsafeSendJson(WebsocketMessage{
				Event: ErrorEvent,
				Args:  []string{"could not authenticate client: " + err.Error()},
			})

			return nil
		}
	}

	switch m.Event {
	case AuthenticationEvent:
		{
			token, err := ParseJWT([]byte(strings.Join(m.Args, "")))
			if err != nil {
				return err
			}

			if token.HasPermission(PermissionConnect) {
				wsh.JWT = token
			}

			// On every authentication event, send the current server status back
			// to the client. :)
			wsh.Server.Events().Publish(server.StatusEvent, wsh.Server.State)

			wsh.unsafeSendJson(WebsocketMessage{
				Event: AuthenticationSuccessEvent,
				Args:  []string{},
			})

			return nil
		}
	case SetStateEvent:
		{
			if !wsh.JWT.HasPermission(PermissionSendPower) {
				return nil
			}

			switch strings.Join(m.Args, "") {
			case "start":
				return wsh.Server.Environment.Start()
			case "stop":
				return wsh.Server.Environment.Stop()
			case "restart":
				{
					if err := wsh.Server.Environment.WaitForStop(60, false); err != nil {
						return err
					}

					return wsh.Server.Environment.Start()
				}
			case "kill":
				return wsh.Server.Environment.Terminate(os.Kill)
			}

			return nil
		}
	case SendServerLogsEvent:
		{
			if running, _ := wsh.Server.Environment.IsRunning(); !running {
				return nil
			}

			logs, err := wsh.Server.Environment.Readlog(1024 * 16)
			if err != nil {
				return err
			}

			for _, line := range logs {
				wsh.SendJson(&WebsocketMessage{
					Event: server.ConsoleOutputEvent,
					Args:  []string{line},
				})
			}

			return nil
		}
	case SendCommandEvent:
		{
			if !wsh.JWT.HasPermission(PermissionSendCommand) {
				return nil
			}

			if wsh.Server.State == server.ProcessOfflineState {
				return nil
			}

			return wsh.Server.Environment.SendCommand(strings.Join(m.Args, ""))
		}
	}

	return nil
}
