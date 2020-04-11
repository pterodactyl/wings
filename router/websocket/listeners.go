package websocket

import (
	"context"
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
					h.SendJson(&Message{Event: TokenExpiredEvent})
				} else if jwt.ExpirationTime.Unix()-time.Now().Unix() <= 180 {
					h.SendJson(&Message{Event: TokenExpiringEvent})
				}
			}
		}
	}
}

// Listens for different events happening on a server and sends them along
// to the connected websocket.
func (h *Handler) ListenForServerEvents(ctx context.Context) {
	events := []string{
		server.StatsEvent,
		server.StatusEvent,
		server.ConsoleOutputEvent,
		server.InstallOutputEvent,
		server.DaemonMessageEvent,
		server.BackupCompletedEvent,
	}

	eventChannel := make(chan server.Event)
	for _, event := range events {
		h.server.Events().Subscribe(event, eventChannel)
	}

	select {
	case <-ctx.Done():
		for _, event := range events {
			h.server.Events().Unsubscribe(event, eventChannel)
		}

		close(eventChannel)
	default:
		// Listen for different events emitted by the server and respond to them appropriately.
		for d := range eventChannel {
			h.SendJson(&Message{
				Event: d.Topic,
				Args:  []string{d.Data},
			})
		}
	}
}
