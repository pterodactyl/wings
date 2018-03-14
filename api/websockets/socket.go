package websockets

import "github.com/gorilla/websocket"
import (
	"time"

	log "github.com/sirupsen/logrus"
)

type Socket struct {
	collection *Collection

	conn *websocket.Conn

	send chan []byte
}

func (s *Socket) readPump() {
	defer func() {
		s.collection.unregister <- s
		s.conn.Close()
	}()
	s.conn.SetReadLimit(maxMessageSize)
	s.conn.SetReadDeadline(time.Now().Add(pongWait))
	s.conn.SetPongHandler(func(string) error {
		s.conn.SetReadDeadline(time.Now().Add(pongWait))
		return nil
	})
	for {
		t, m, err := s.conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway) {
				log.WithError(err).Debug("Websocket closed unexpectedly.")
			}
			return
		}
		// Handle websocket responses somehow
		if t == websocket.TextMessage {
			log.Debug("Received websocket message: " + string(m))
		}
	}
}

func (s *Socket) writePump() {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		s.conn.Close()
	}()
	for {
		select {
		case m, ok := <-s.send:
			s.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok {
				// The collection closed the channel
				s.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}
			w, err := s.conn.NextWriter(websocket.TextMessage)
			if err != nil {
				return
			}
			w.Write([]byte{'['})
			w.Write(m)
			n := len(s.send) - 1
			for i := 0; i < n; i++ {
				w.Write([]byte{','})
				w.Write(<-s.send)
			}
			w.Write([]byte{']'})
			if err := w.Close(); err != nil {
				return
			}
		case <-ticker.C:
			s.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := s.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}
