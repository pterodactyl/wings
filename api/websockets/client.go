package websockets

import "github.com/gorilla/websocket"
import (
	"time"

	log "github.com/sirupsen/logrus"
)

type Client struct {
	hub *Hub

	socket *websocket.Conn

	send chan []byte
}

func (c *Client) readPump() {
	defer func() {
		c.hub.unregister <- c
		c.socket.Close()
	}()
	c.socket.SetReadLimit(maxMessageSize)
	c.socket.SetReadDeadline(time.Now().Add(pongWait))
	c.socket.SetPongHandler(func(string) error {
		c.socket.SetReadDeadline(time.Now().Add(pongWait))
		return nil
	})
	for {
		_, _, err := c.socket.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway) {
				log.WithError(err).Debug("Websocket closed unexpectedly.")
			}
			return
		}
	}
}

func (c *Client) writePump() {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		c.socket.Close()
	}()
	for {
		select {
		case m, ok := <-c.send:
			c.socket.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok {
				// The hub closed the channel
				c.socket.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}
			w, err := c.socket.NextWriter(websocket.TextMessage)
			if err != nil {
				return
			}
			w.Write([]byte{'['})
			w.Write(m)
			for i := 0; i < len(c.send)+1; i++ {
				w.Write([]byte{','})
				w.Write(<-c.send)
			}
			w.Write([]byte{']'})
			if err := w.Close(); err != nil {
				return
			}
		case <-ticker.C:
			c.socket.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.socket.WriteMessage(websocket.PingMessage, []byte{}); err != nil {
				return
			}
		}
	}
}
