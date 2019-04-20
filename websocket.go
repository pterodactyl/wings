package main

import (
	"errors"
	"fmt"
	"github.com/gorilla/websocket"
	"github.com/julienschmidt/httprouter"
	"github.com/pterodactyl/wings/server"
	"go.uber.org/zap"
	"net/http"
	"os"
	"strings"
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

	server *server.Server

	inbound bool
}

func (rt *Router) routeWebsocket(w http.ResponseWriter, r *http.Request, ps httprouter.Params) {
	c, err := rt.upgrader.Upgrade(w, r, nil)
	if err != nil {
		zap.S().Error(err)
		return
	}
	defer c.Close()

	s := rt.Servers.Get(ps.ByName("server"))

	for {
		j := WebsocketMessage{server: s, inbound: true}

		if _, _, err := c.ReadMessage(); err != nil {
			if !websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway, websocket.CloseServiceRestart) {
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

		fmt.Printf("%s received: %s = %s\n", s.Uuid, j.Event, strings.Join(j.Args, " "))
		if err := j.HandleInbound(c); err != nil {
			zap.S().Warnw("error handling inbound websocket request", zap.Error(err))
			break
		}
	}

	zap.S().Debugw("disconnected from instance", zap.String("ip", c.RemoteAddr().String()))
}

func (wsm *WebsocketMessage) HandleInbound(c *websocket.Conn) error {
	if !wsm.inbound {
		return errors.New("cannot handle websocket message, not an inbound connection")
	}

	switch wsm.Event {
	case "set state":
		{
			var err error
			switch strings.Join(wsm.Args, "") {
			case "start":
				err = wsm.server.Environment().Start()
				break
			case "stop":
				err = wsm.server.Environment().Stop()
				break
			case "restart":
				err = wsm.server.Environment().Terminate(os.Kill)
				break
			}

			if err != nil {
				return err
			}
		}
	case "send logs":
		{
			logs, err := wsm.server.Environment().Readlog(1024 * 5)
			if err != nil {
				return err
			}

			for _, line := range logs {
				c.WriteJSON(&WebsocketMessage{
					Event: "console output",
					Args: []string{line},
				})
			}

			return nil
		}
	case "send command":
		{
			return nil
		}
	}

	return nil
}