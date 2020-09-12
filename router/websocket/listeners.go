package websocket

import (
	"context"
	"github.com/pterodactyl/wings/events"
	"github.com/pterodactyl/wings/server"
	"time"
)

// Checks the time to expiration on the JWT every 30 seconds until the token has
// expired. If we are within 3 minutes of the token expiring, send a notice over
// the socket that it is expiring soon. If it has expired, send that notice as well.
func (h *Handler) ListenForExpiration(ctx context.Context) {
	// Make a ticker and completion channel that is used to continuously poll the
	// JWT stored in the session to send events to the socket when it is expiring.
	ticker := time.NewTicker(time.Second * 30)

	// Whenever this function is complete, end the ticker, close out the channel,
	// and then close the websocket connection.
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
}

// Listens for different events happening on a server and sends them along
// to the connected websocket.
func (h *Handler) ListenForServerEvents(ctx context.Context) {
	h.server.Log().Debug("listening for server events over websocket")

	eventChannel := make(chan events.Event)
	h.server.Events().Subscribe(e, eventChannel)

	go func(ctx context.Context) {
		select {
		case <-ctx.Done():
			h.server.Events().Unsubscribe(e, eventChannel)

			close(eventChannel)
		}
	}(ctx)

	for d := range eventChannel {
		if err := h.SendJson(&Message{Event: d.Topic, Args: []string{d.Data}}); err != nil {
			h.server.Log().WithField("error", err).Warn("error while sending server data over websocket")
		}
	}
}
