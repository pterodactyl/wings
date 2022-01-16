package websocket

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"emperror.dev/errors"
	"github.com/apex/log"
	"github.com/pterodactyl/wings/events"
	"github.com/pterodactyl/wings/server"
)

// RegisterListenerEvents will setup the server event listeners and expiration
// timers. This is only triggered on first authentication with the websocket,
// reconnections will not call it.
//
// This needs to be called once the socket is properly authenticated otherwise
// you'll get a flood of error spam in the output that doesn't make sense because
// Docker events being output to the socket will fail when it hasn't been
// properly initialized yet.
//
// @see https://github.com/pterodactyl/panel/issues/3295
func (h *Handler) registerListenerEvents(ctx context.Context) {
	h.Logger().Debug("registering event listeners for connection")

	go func() {
		if err := h.listenForServerEvents(ctx); err != nil {
			h.Logger().Warn("error while processing server event; closing websocket connection")
			if err := h.Connection.Close(); err != nil {
				h.Logger().WithField("error", errors.WithStack(err)).Error("error closing websocket connection")
			}
		}
	}()

	go h.listenForExpiration(ctx)
}

// ListenForExpiration checks the time to expiration on the JWT every 30 seconds
// until the token has expired. If we are within 3 minutes of the token expiring,
// send a notice over the socket that it is expiring soon. If it has expired,
// send that notice as well.
func (h *Handler) listenForExpiration(ctx context.Context) {
	// Make a ticker and completion channel that is used to continuously poll the
	// JWT stored in the session to send events to the socket when it is expiring.
	ticker := time.NewTicker(time.Second * 30)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			jwt := h.GetJwt()
			if jwt != nil {
				if jwt.ExpirationTime.Unix()-time.Now().Unix() <= 0 {
					_ = h.SendJson(Message{Event: TokenExpiredEvent})
				} else if jwt.ExpirationTime.Unix()-time.Now().Unix() <= 60 {
					_ = h.SendJson(Message{Event: TokenExpiringEvent})
				}
			}
		}
	}
}

var e = []string{
	server.StatsEvent,
	server.StatusEvent,
	server.ConsoleOutputEvent,
	server.InstallOutputEvent,
	server.InstallStartedEvent,
	server.InstallCompletedEvent,
	server.DaemonMessageEvent,
	server.BackupCompletedEvent,
	server.BackupRestoreCompletedEvent,
	server.TransferLogsEvent,
	server.TransferStatusEvent,
}

// ListenForServerEvents will listen for different events happening on a server
// and send them along to the connected websocket client. This function will
// block until the context provided to it is canceled.
func (h *Handler) listenForServerEvents(ctx context.Context) error {
	defer log.Error("listenForServerEvents: closed")
	var o sync.Once
	var err error

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	c := make(chan events.Event)

	// Subscribe to all of the events with the same callback that will push the
	// data out over the websocket for the server.
	h.server.Events().On(c, e...)

	c2 := make(chan []byte)
	h.server.LogOutputOn(c2)

	for {
		select {
		case <-ctx.Done():
			break
		case e := <-c2:
			sendErr := h.SendJson(Message{Event: server.ConsoleOutputEvent, Args: []string{string(e)}})
			if sendErr == nil {
				continue
			}

			h.Logger().WithField("event", server.ConsoleOutputEvent).WithField("error", sendErr).Error("failed to send event over server websocket")
			// Avoid race conditions by only setting the error once and then canceling
			// the context. This way if additional processing errors come through due
			// to a massive flood of things you still only report and stop at the first.
			o.Do(func() {
				err = sendErr
			})
			cancel()
		case e := <-c:
			var sendErr error
			message := Message{Event: e.Topic}
			if str, ok := e.Data.(string); ok {
				message.Args = []string{str}
			} else if b, ok := e.Data.([]byte); ok {
				message.Args = []string{string(b)}
			} else {
				b, sendErr = json.Marshal(e.Data)
				if sendErr == nil {
					message.Args = []string{string(b)}
				}
			}

			if sendErr == nil {
				sendErr = h.SendJson(message)
			}

			if sendErr == nil {
				continue
			}

			h.Logger().WithField("event", e.Topic).WithField("error", sendErr).Error("failed to send event over server websocket")
			// Avoid race conditions by only setting the error once and then canceling
			// the context. This way if additional processing errors come through due
			// to a massive flood of things you still only report and stop at the first.
			o.Do(func() {
				err = sendErr
			})
			cancel()
		}
		break
	}

	h.server.Events().Off(c, e...)
	close(c)

	h.server.LogOutputOff(c2)
	close(c2)

	// If the internal context is stopped it is either because the parent context
	// got canceled or because we ran into an error. If the "err" variable is nil
	// we can assume the parent was canceled and need not perform any actions.
	if err != nil {
		return errors.WithStack(err)
	}

	return nil
}
