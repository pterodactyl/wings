package websocket

import (
	"context"
	"sync"
	"time"

	"emperror.dev/errors"
	"github.com/goccy/go-json"
	"github.com/pterodactyl/wings/events"
	"github.com/pterodactyl/wings/system"

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
	var o sync.Once
	var err error

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	eventChan := make(chan []byte)
	logOutput := make(chan []byte, 8)
	installOutput := make(chan []byte, 4)

	h.server.Events().On(eventChan) // TODO: make a sinky
	h.server.Sink(system.LogSink).On(logOutput)
	h.server.Sink(system.InstallSink).On(installOutput)

	onError := func(evt string, err2 error) {
		h.Logger().WithField("event", evt).WithField("error", err2).Error("failed to send event over server websocket")
		// Avoid race conditions by only setting the error once and then canceling
		// the context. This way if additional processing errors come through due
		// to a massive flood of things you still only report and stop at the first.
		o.Do(func() {
			err = err2
		})
		cancel()
	}

	for {
		select {
		case <-ctx.Done():
			break
		case b := <-logOutput:
			sendErr := h.SendJson(Message{Event: server.ConsoleOutputEvent, Args: []string{string(b)}})
			if sendErr == nil {
				continue
			}
			onError(server.ConsoleOutputEvent, sendErr)
		case b := <-installOutput:
			sendErr := h.SendJson(Message{Event: server.InstallOutputEvent, Args: []string{string(b)}})
			if sendErr == nil {
				continue
			}
			onError(server.InstallOutputEvent, sendErr)
		case b := <-eventChan:
			var e events.Event
			if err := events.DecodeTo(b, &e); err != nil {
				continue
			}
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
				if sendErr == nil {
					continue
				}
			}
			onError(message.Event, sendErr)
		}
		break
	}

	// These functions will automatically close the channel if it hasn't been already.
	h.server.Events().Off(eventChan)
	h.server.Sink(system.LogSink).Off(logOutput)
	h.server.Sink(system.InstallSink).Off(installOutput)

	// If the internal context is stopped it is either because the parent context
	// got canceled or because we ran into an error. If the "err" variable is nil
	// we can assume the parent was canceled and need not perform any actions.
	if err != nil {
		return errors.WithStack(err)
	}

	return nil
}
