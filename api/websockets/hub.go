package websockets

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
	log "github.com/sirupsen/logrus"
)

const (
	writeWait = 10 * time.Second
	pongWait  = 60 * time.Second

	pingPeriod = pongWait * 9 / 10

	maxMessageSize = 512
)

var wsupgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

type websocketMap map[*Client]bool

type Hub struct {
	clients websocketMap

	Broadcast chan Message

	register   chan *Client
	unregister chan *Client
	close      chan bool
}

//var _ io.Writer = &Hub{}

func NewHub() *Hub {
	return &Hub{
		Broadcast:  make(chan Message),
		register:   make(chan *Client),
		unregister: make(chan *Client),
		close:      make(chan bool),
		clients:    make(websocketMap),
	}
}

func (h *Hub) Upgrade(w http.ResponseWriter, r *http.Request) {
	socket, err := wsupgrader.Upgrade(w, r, nil)
	if err != nil {
		log.WithError(err).Error("Failed to upgrade to websocket")
		return
	}
	c := &Client{
		hub:    h,
		socket: socket,
		send:   make(chan []byte, 256),
	}
	h.register <- c

	go c.readPump()
	go c.writePump()
}

func (h *Hub) Subscribe(c *Client) {
	h.register <- c
}

func (h *Hub) Unsubscribe(c *Client) {
	h.unregister <- c
}

func (h *Hub) Run() {
	defer func() {
		for s := range h.clients {
			close(s.send)
			delete(h.clients, s)
		}
		close(h.register)
		close(h.unregister)
		close(h.Broadcast)
		close(h.close)
	}()
	for {
		select {
		case s := <-h.register:
			h.clients[s] = true
		case s := <-h.unregister:
			if _, ok := h.clients[s]; ok {
				delete(h.clients, s)
				close(s.send)
			}
		case m := <-h.Broadcast:
			b, err := json.Marshal(m)
			if err != nil {
				log.WithError(err).Error("Failed to encode websocket message.")
				continue
			}
			for s := range h.clients {
				select {
				case s.send <- b:
				default:
					close(s.send)
					delete(h.clients, s)
				}
			}
		case <-h.close:
			return
		}
	}
}

func (h *Hub) Close() {
	h.close <- true
}
