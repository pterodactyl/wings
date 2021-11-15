package websocket

import (
	"context"
	"sync"
	"time"

	"emperror.dev/errors"
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
					_ = h.SendJson(&Message{Event: TokenExpiredEvent})
				} else if jwt.ExpirationTime.Unix()-time.Now().Unix() <= 60 {
					_ = h.SendJson(&Message{Event: TokenExpiringEvent})
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
func (h *Handler) listenForServerEvents(pctx context.Context) error {
	var o sync.Once
	var err error
	ctx, cancel := context.WithCancel(pctx)

	callback := func(e events.Event) {
		if sendErr := h.SendJson(&Message{Event: e.Topic, Args: []string{e.Data}}); sendErr != nil {
			h.Logger().WithField("event", e.Topic).WithField("error", sendErr).Error("failed to send event over server websocket")
			// Avoid race conditions by only setting the error once and then canceling
			// the context. This way if additional processing errors come through due
			// to a massive flood of things you still only report and stop at the first.
			o.Do(func() {
				err = sendErr
				cancel()
			})
		}
	}

	// Subscribe to all of the events with the same callback that will push the
	// data out over the websocket for the server.
	for _, evt := range e {
		h.server.Events().On(evt, &callback)
	}

	// When this function returns de-register all of the event listeners.
	defer func() {
		for _, evt := range e {
			h.server.Events().Off(evt, &callback)
		}
	}()

	<-ctx.Done()
	// If the internal context is stopped it is either because the parent context
	// got canceled or because we ran into an error. If the "err" variable is nil
	// we can assume the parent was canceled and need not perform any actions.
	if err != nil {
		return errors.WithStack(err)
	}

	return nil
}
