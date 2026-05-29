package router

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"emperror.dev/errors"
	"github.com/gin-gonic/gin"
	ws "github.com/gorilla/websocket"
	"github.com/pterodactyl/wings/router/middleware"
	"github.com/pterodactyl/wings/router/websocket"
	"github.com/pterodactyl/wings/server"
	"golang.org/x/time/rate"
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

	// Limit the total number of websockets that can be opened at any one time for
	// a server instance. This applies across all users connected to the server, and
	// is not applied on a per-user basis.
	//
	// todo: it would be great to make this per-user instead, but we need to modify
	//  how we even request this endpoint in order for that to be possible. Some type
	//  of signed identifier in the URL that is verified on this end and set by the
	//  panel using a shared secret is likely the easiest option. The benefit of that
	//  is that we can both scope things to the user before authentication, and also
	//  verify that the JWT provided by the panel is assigned to the same user.
	if s.Websockets().Len() >= 30 {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
			"error": "Too many open websocket connections.",
		})

		return
	}

	c.Header("Content-Security-Policy", "default-src 'self'")
	c.Header("X-Frame-Options", "DENY")

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

	// There is a separate rate limiter that applies to individual message types
	// within the actual websocket logic handler. _This_ rate limiter just exists
	// to avoid enormous floods of data through the socket since we need to parse
	// JSON each time. This rate limit realistically should never be hit since this
	// would require sending 50+ messages a second over the websocket (no more than
	// 10 per 200ms).
	var throttled bool
	rl := rate.NewLimiter(rate.Every(time.Millisecond*200), 10)

	for {
		t, p, err := handler.Connection.ReadMessage()
		if err != nil {
			if ws.IsUnexpectedCloseError(err, expectedCloseCodes...) {
				handler.Logger().WithField("error", err).Warn("error handling websocket message for server")
			}
			break
		}

		if !rl.Allow() {
			if !throttled {
				throttled = true
				_ = handler.Connection.WriteJSON(websocket.Message{Event: websocket.ThrottledEvent, Args: []string{"global"}})
			}
			continue
		}

		throttled = false

		// If the message isn't a format we expect, or the length of the message is far larger
		// than we'd ever expect, drop it. The websocket upgrader logic does enforce a maximum
		// _compressed_ message size of 4Kb but that could decompress to a much larger amount
		// of data.
		if t != ws.TextMessage || len(p) > 32_768 {
			continue
		}

		// Discard and JSON parse errors into the void and don't continue processing this
		// specific socket request. If we did a break here the client would get disconnected
		// from the socket, which is NOT what we want to do.
		var j websocket.Message
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
