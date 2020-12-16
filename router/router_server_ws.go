package router

import (
	"context"
	"encoding/json"
	"github.com/gin-gonic/gin"
	ws "github.com/gorilla/websocket"
	"github.com/pterodactyl/wings/router/websocket"
	"time"
)

// Upgrades a connection to a websocket and passes events along between.
func getServerWebsocket(c *gin.Context) {
	s := GetServer(c.Param("server"))
	handler, err := websocket.GetHandler(s, c.Writer, c.Request)
	if err != nil {
		NewServerError(err, s).Abort(c)
		return
	}
	defer handler.Connection.Close()

	// Create a context that can be canceled when the user disconnects from this
	// socket that will also cancel listeners running in separate threads.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Track this open connection on the server so that we can close them all programmatically
	// if the server is deleted.
	s.Websockets().Push(handler.Uuid(), &cancel)
	defer s.Websockets().Remove(handler.Uuid())

	// Listen for the context being canceled and then close the websocket connection. This normally
	// just happens because you're disconnecting from the socket in the browser, however in some
	// cases we close the connections programmatically (e.g. deleting the server) and need to send
	// a close message to the websocket so it disconnects.
	go func(ctx context.Context, c *ws.Conn) {
	ListenerLoop:
		for {
			select {
			case <-ctx.Done():
				handler.Connection.WriteControl(ws.CloseMessage, ws.FormatCloseMessage(ws.CloseGoingAway, "server deleted"), time.Now().Add(time.Second*5))
				// A break right here without defining the specific loop would only break the select
				// and not actually break the for loop, thus causing this routine to stick around forever.
				break ListenerLoop
			}
		}
	}(ctx, handler.Connection)

	go handler.ListenForServerEvents(ctx)
	go handler.ListenForExpiration(ctx)

	for {
		j := websocket.Message{}

		_, p, err := handler.Connection.ReadMessage()
		if err != nil {
			if !ws.IsCloseError(
				err,
				ws.CloseNormalClosure,
				ws.CloseGoingAway,
				ws.CloseNoStatusReceived,
				ws.CloseServiceRestart,
				ws.CloseAbnormalClosure,
			) {
				s.Log().WithField("error", err).Warn("error handling websocket message for server")
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
			if err := handler.HandleInbound(msg); err != nil {
				handler.SendErrorJson(msg, err)
			}
		}(j)
	}
}
