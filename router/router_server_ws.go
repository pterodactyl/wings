package router

import (
	"context"
	"encoding/json"
	"time"

	"github.com/gin-gonic/gin"
	ws "github.com/gorilla/websocket"

	"github.com/pterodactyl/wings/router/middleware"
	"github.com/pterodactyl/wings/router/websocket"
)

var expectedCloseCodes = []int{
	ws.CloseGoingAway,
	ws.CloseAbnormalClosure,
	ws.CloseNormalClosure,
	ws.CloseNoStatusReceived,
	ws.CloseServiceRestart,
}

// Upgrades a connection to a websocket and passes events along between.
func getServerWebsocket(c *gin.Context) {
	manager := middleware.ExtractManager(c)
	s, _ := manager.Get(c.Param("server"))

	// Create a context that can be canceled when the user disconnects from this
	// socket that will also cancel listeners running in separate threads. If the
	// connection itself is terminated listeners using this context will also be
	// closed.
	ctx, cancel := context.WithCancel(c.Request.Context())
	defer cancel()

	handler, err := websocket.GetHandler(s, c.Writer, c.Request)
	if err != nil {
		NewServerError(err, s).Abort(c)
		return
	}
	defer handler.Connection.Close()

	// Track this open connection on the server so that we can close them all programmatically
	// if the server is deleted.
	s.Websockets().Push(handler.Uuid(), &cancel)
	handler.Logger().Debug("opening connection to server websocket")

	defer func() {
		s.Websockets().Remove(handler.Uuid())
		handler.Logger().Debug("closing connection to server websocket")
	}()

	// If the server is deleted we need to send a close message to the connected client
	// so that they disconnect since there will be no more events sent along. Listen for
	// the request context being closed to break this loop, otherwise this routine will
	// be left hanging in the background.
	go func() {
		select {
		case <-ctx.Done():
			break
		case <-s.Context().Done():
			handler.Connection.WriteControl(ws.CloseMessage, ws.FormatCloseMessage(ws.CloseGoingAway, "server deleted"), time.Now().Add(time.Second*5))
			break
		}
	}()

	for {
		j := websocket.Message{}

		_, p, err := handler.Connection.ReadMessage()
		if err != nil {
			if ws.IsUnexpectedCloseError(err, expectedCloseCodes...) {
				handler.Logger().WithField("error", err).Warn("error handling websocket message for server")
			}
			break
		}

		// Discard and JSON parse errors into the void and don't continue processing this
		// specific socket request. If we did a break here the client would get disconnected
		// from the socket, which is NOT what we want to do.
		if err := json.Unmarshal(p, &j); err != nil {
			continue
		}

		go func(msg websocket.Message) {
			if err := handler.HandleInbound(ctx, msg); err != nil {
				handler.SendErrorJson(msg, err)
			}
		}(j)
	}
}
