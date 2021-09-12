package websocket

import (
	"context"
	"time"

	"github.com/pterodactyl/wings/events"
	"github.com/pterodactyl/wings/server"
)

// ListenForExpiration checks the time to expiration on the JWT every 30 seconds
// until the token has expired. If we are within 3 minutes of the token expiring,
// send a notice over the socket that it is expiring soon. If it has expired,
// send that notice as well.
func (h *Handler) ListenForExpiration(ctx context.Context) {
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
func (h *Handler) ListenForServerEvents(ctx context.Context) {
	h.server.Log().Debug("listening for server events over websocket")
	callback := func(e events.Event) {
		if err := h.SendJson(&Message{Event: e.Topic, Args: []string{e.Data}}); err != nil {
			h.server.Log().WithField("error", err).Warn("error while sending server data over websocket")
		}
	}

	// Subscribe to all of the events with the same callback that will push the data out over the
	// websocket for the server.
	for _, evt := range e {
		h.server.Events().On(evt, &callback)
	}

	<-ctx.Done()
	// Block until the context is stopped and then de-register all of the event listeners
	// that we registered earlier.
	for _, evt := range e {
		h.server.Events().Off(evt, &callback)
	}
}
