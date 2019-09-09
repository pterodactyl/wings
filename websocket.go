package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"github.com/gorilla/websocket"
	"github.com/julienschmidt/httprouter"
	"github.com/pterodactyl/wings/config"
	"github.com/pterodactyl/wings/server"
	"go.uber.org/zap"
	"net/http"
	"os"
	"strings"
	"sync"
)

const (
	SetStateEvent       = "set state"
	SendServerLogsEvent = "send logs"
	SendCommandEvent    = "send command"
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

	inbound bool
}

type WebsocketHandler struct {
	Server     *server.Server
	Mutex      sync.Mutex
	Connection *websocket.Conn
}

type socketCredentials struct {
	ServerUuid string `json:"server_uuid"`
}

func (rt *Router) AuthenticateWebsocket(h httprouter.Handle) httprouter.Handle {
	return func(w http.ResponseWriter, r *http.Request, ps httprouter.Params) {
		s := rt.Servers.Get(ps.ByName("server"))

		j, err := json.Marshal(socketCredentials{ServerUuid: s.Uuid})
		if err != nil {
			zap.S().Errorw("failed to marshal json", zap.Error(err))
			http.Error(w, "failed to marshal json", http.StatusInternalServerError)
			return
		}

		url := strings.TrimRight(config.Get().PanelLocation, "/") + "/api/remote/websocket/" + ps.ByName("token")
		req, err := http.NewRequest(http.MethodPost, url, bytes.NewBuffer(j))
		if err != nil {
			zap.S().Errorw("failed to generate a new HTTP request when validating websocket credentials", zap.Error(err))
			http.Error(w, "failed to generate HTTP request", http.StatusInternalServerError)
			return
		}

		req.Header.Set("Accept", "application/json")
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+config.Get().AuthenticationToken)

		client := &http.Client{}
		resp, err := client.Do(req)
		if err != nil {
			zap.S().Errorw("failed to perform client HTTP request", zap.Error(err))
			http.Error(w, "failed to perform client HTTP request", http.StatusInternalServerError)
			return
		}

		defer resp.Body.Close()
		if resp.StatusCode != http.StatusNoContent {
			http.Error(w, "failed to validate token with server", resp.StatusCode)
			return
		}

		h(w, r, ps)
	}
}

// Handle a request for a specific server websocket. This will handle inbound requests as well
// as ensure that any console output is also passed down the wire on the socket.
func (rt *Router) routeWebsocket(w http.ResponseWriter, r *http.Request, ps httprouter.Params) {
	c, err := rt.upgrader.Upgrade(w, r, nil)
	if err != nil {
		zap.S().Error(err)
		return
	}
	defer c.Close()

	s := rt.Servers.Get(ps.ByName("server"))
	handler := WebsocketHandler{
		Server:     s,
		Mutex:      sync.Mutex{},
		Connection: c,
	}

	handleOutput := func(data string) {
		handler.SendJson(&WebsocketMessage{
			Event: server.ConsoleOutputEvent,
			Args:  []string{data},
		})
	}

	handleServerStatus := func(data string) {
		handler.SendJson(&WebsocketMessage{
			Event: server.StatusEvent,
			Args:  []string{data},
		})
	}

	handleResourceUse := func(data string) {
		handler.SendJson(&WebsocketMessage{
			Event: server.StatsEvent,
			Args:  []string{data},
		})
	}

	s.AddListener(server.StatusEvent, &handleServerStatus)
	defer s.RemoveListener(server.StatusEvent, &handleServerStatus)

	s.AddListener(server.ConsoleOutputEvent, &handleOutput)
	defer s.RemoveListener(server.ConsoleOutputEvent, &handleOutput)

	s.AddListener(server.StatsEvent, &handleResourceUse)
	defer s.RemoveListener(server.StatsEvent, &handleResourceUse)

	s.Emit(server.StatusEvent, s.State)

	for {
		j := WebsocketMessage{inbound: true}

		_, p, err := c.ReadMessage()
		if err != nil {
			if !websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway, websocket.CloseNoStatusReceived, websocket.CloseServiceRestart) {
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
			zap.S().Warnw("error handling inbound websocket request", zap.Error(err))
			break
		}
	}
}

// Perform a blocking send operation on the websocket since we want to avoid any
// concurrent writes to the connection, which would cause a runtime panic and cause
// the program to crash out.
func (wsh *WebsocketHandler) SendJson(v interface{}) error {
	wsh.Mutex.Lock()
	defer wsh.Mutex.Unlock()

	return wsh.Connection.WriteJSON(v)
}

// Handle the inbound socket request and route it to the proper server action.
func (wsh *WebsocketHandler) HandleInbound(m WebsocketMessage) error {
	if !m.inbound {
		return errors.New("cannot handle websocket message, not an inbound connection")
	}

	switch m.Event {
	case SetStateEvent:
		{
			var err error
			switch strings.Join(m.Args, "") {
			case "start":
				err = wsh.Server.Environment.Start()
				break
			case "stop":
				err = wsh.Server.Environment.Stop()
				break
			case "restart":
				break
			case "kill":
				err = wsh.Server.Environment.Terminate(os.Kill)
				break
			}

			if err != nil {
				return err
			}
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
			return wsh.Server.Environment.SendCommand(strings.Join(m.Args, ""))
		}
	}

	return nil
}
