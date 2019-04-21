package main

import (
	"errors"
	"github.com/gorilla/websocket"
	"github.com/julienschmidt/httprouter"
	"github.com/pterodactyl/wings/server"
	"go.uber.org/zap"
	"net/http"
	"os"
	"strings"
	"sync"
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
		Server: s,
		Mutex:  sync.Mutex{},
		Connection: c,
	}

	handleOutput := func(data string) {
		handler.SendJson(&WebsocketMessage{
			Event: "console output",
			Args:  []string{data},
		})
	}

	s.AddListener(server.ConsoleOutputEvent, &handleOutput)
	defer s.RemoveListener(server.ConsoleOutputEvent, &handleOutput)

	for {
		j := WebsocketMessage{inbound: true}

		if _, _, err := c.ReadMessage(); err != nil {
			if !websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway, websocket.CloseNoStatusReceived, websocket.CloseServiceRestart) {
				zap.S().Errorw("error handling websocket message", zap.Error(err))
			}
			break
		}

		// Discard and JSON parse errors into the void and don't continue processing this
		// specific socket request. If we did a break here the client would get disconnected
		// from the socket, which is NOT what we want to do.
		if err := c.ReadJSON(&j); err != nil {
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
	case "set state":
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
				err = wsh.Server.Environment.Terminate(os.Kill)
				break
			}

			if err != nil {
				return err
			}
		}
	case "send logs":
		{
			logs, err := wsh.Server.Environment.Readlog(1024 * 5)
			if err != nil {
				return err
			}

			for _, line := range logs {
				wsh.SendJson(&WebsocketMessage{
					Event: "console output",
					Args:  []string{line},
				})
			}

			return nil
		}
	case "send command":
		{
			return wsh.Server.Environment.SendCommand(strings.Join(m.Args, ""))
		}
	}

	return nil
}
