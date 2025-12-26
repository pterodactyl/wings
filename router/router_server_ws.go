package router

import (
	"context"
	"encoding/json"

	"emperror.dev/errors"
	"github.com/gin-gonic/gin"
	ws "github.com/gorilla/websocket"
	"github.com/pterodactyl/wings/router/middleware"
	"github.com/pterodactyl/wings/router/websocket"
	"github.com/pterodactyl/wings/server"
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

	handler, err := websocket.GetHandler(s, c.Writer, c.Request, c)
	if err != nil {
		middleware.CaptureAndAbort(c, err)
		return
	}

	// Track this open connection on the server so that we can close them all programmatically
	// if the server is deleted.
	s.Websockets().Push(handler.Uuid(), &cancel)
	handler.Logger().Debug("opening connection to server websocket")
	defer s.Websockets().Remove(handler.Uuid())

	go func() {
		select {
		// When the main context is canceled (through disconnect, server deletion, or server
		// suspension) close the connection itself.
		case <-ctx.Done():
			handler.Logger().Debug("closing connection to server websocket")
			if err := handler.Connection.Close(); err != nil {
				handler.Logger().WithError(err).Error("failed to close websocket connection")
			}
			break
		}
	}()

	go func() {
		select {
		case <-ctx.Done():
			return
		// If the server is deleted we need to send a close message to the connected client
		// so that they disconnect since there will be no more events sent along. Listen for
		// the request context being closed to break this loop, otherwise this routine will
		// be left hanging in the background.
		case <-s.Context().Done():
			cancel()
			break
		}
	}()

	// Due to how websockets are handled we need to connect to the socket
	// and _then_ abort it if the server is suspended. You cannot capture
	// the HTTP response in the websocket client, thus we connect and then
	// immediately close with failure.
	if s.IsSuspended() {
		_ = handler.Connection.WriteMessage(ws.CloseMessage, ws.FormatCloseMessage(4409, "server is suspended"))

		return
	}

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
				if errors.Is(err, server.ErrSuspended) {
					cancel()
				} else {
					_ = handler.SendErrorJson(msg, err)
				}
			}
		}(j)
	}
}
