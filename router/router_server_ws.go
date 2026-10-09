package router

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
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

var (
	// maxWebsocketConnections is the maximum number of authenticated websocket
	// connections a server can have open at once.
	maxWebsocketConnections = 30
	// websocketAuthenticationTimeout is the amount of time a websocket connection
	// has to authenticate before it is closed.
	websocketAuthenticationTimeout = 30 * time.Second

	errTooManyWebsockets = errors.New("too many open websocket connections")
)

// getServerWebsocket returns a handler that upgrades a connection to a websocket
// and passes events along between, allowing at most maxConnections authenticated
// connections to a server, each of which must authenticate within authTimeout.
func getServerWebsocket(maxConnections int, authTimeout time.Duration) gin.HandlerFunc {
	return func(c *gin.Context) {
		handleServerWebsocket(c, maxConnections, authTimeout)
	}
}

func handleServerWebsocket(c *gin.Context, maxConnections int, authTimeout time.Duration) {
	manager := middleware.ExtractManager(c)
	s, _ := manager.Get(c.Param("server"))

	// Limit the total number of authenticated websockets that can be open at any one
	// time for a server instance. This applies across all users connected to the server,
	// and is not applied on a per-user basis.
	//
	// todo: it would be great to make this per-user instead, but we need to modify
	//  how we even request this endpoint in order for that to be possible. Some type
	//  of signed identifier in the URL that is verified on this end and set by the
	//  panel using a shared secret is likely the easiest option. The benefit of that
	//  is that we can both scope things to the user before authentication, and also
	//  verify that the JWT provided by the panel is assigned to the same user.
	if s.Websockets().Len() >= maxConnections {
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

	// Track this connection on the server once it authenticates so that we can close them
	// all programmatically when access to the server is revoked. Connections only count
	// towards the limit once they have authenticated, and those that do not
	// authenticate in time are closed.
	//
	// Messages are handled in their own goroutines which can outlive this function, so
	// a connection that authenticates after it has closed is not tracked.
	var (
		trackMu sync.Mutex
		closed  bool
	)
	_ = handler.Connection.SetReadDeadline(time.Now().Add(authTimeout))
	handler.OnAuthenticated(func() error {
		trackMu.Lock()
		defer trackMu.Unlock()
		if closed {
			return context.Canceled
		}
		if !s.Websockets().TryPush(handler.Uuid(), &cancel, maxConnections) {
			cancel()
			return errTooManyWebsockets
		}
		if err := handler.Connection.SetReadDeadline(time.Time{}); err != nil {
			s.Websockets().Remove(handler.Uuid())
			return err
		}
		return nil
	})
	handler.Logger().Debug("opening connection to server websocket")
	defer func() {
		trackMu.Lock()
		closed = true
		trackMu.Unlock()
		s.Websockets().Remove(handler.Uuid())
	}()

	go func() {
		// When the main context is canceled (through disconnect, server deletion, or server
		// suspension) close the connection itself.
		<-ctx.Done()
		handler.Logger().Debug("closing connection to server websocket")
		if err := handler.Connection.Close(); err != nil {
			handler.Logger().WithError(err).Error("failed to close websocket connection")
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
