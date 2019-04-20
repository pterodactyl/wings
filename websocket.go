package main

import (
	"fmt"
	"github.com/gorilla/websocket"
	"github.com/julienschmidt/httprouter"
	"go.uber.org/zap"
	"net/http"
)

type WebsocketMessage struct {
	// The action to perform. Should be one of the following that are supported:
	//
	// - status : Returns the server's power state.
	// - logs : Returns the server log data at the time of the request.
	// - power : Performs a power action aganist the server based the data.
	// - command : Performs a command on a server using the data field.
	Action string

	// The data to pass along, only used by power/command currently. Other requests
	// should either omit the field or pass an empty string value as it is ignored.
	Data   string
}

func (rt *Router) routeWebsocket(w http.ResponseWriter, r *http.Request, ps httprouter.Params) {
	c, err := rt.upgrader.Upgrade(w, r, nil)
	if err != nil {
		zap.S().Error(err)
		return
	}
	defer c.Close()

	for {
		j := WebsocketMessage{}

		// Discard and JSON parse errors into the void and don't continue processing this
		// specific socket request. If we did a break here the client would get disconnected
		// from the socket, which is NOT what we want to do.
		if err := c.ReadJSON(&j); err != nil {
			continue
		}

		fmt.Printf("%s sent: %s = %s\n", c.RemoteAddr(), j.Action, j.Data)

		if err := c.WriteMessage(websocket.TextMessage, []byte("")); err != nil {
			zap.S().Warnw("error writing JSON to socket", zap.Error(err))
			continue
		}
	}
}
