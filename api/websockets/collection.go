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

type websocketMap map[*Socket]bool

type Collection struct {
	sockets websocketMap

	Broadcast chan Message

	register   chan *Socket
	unregister chan *Socket
	close      chan bool
}

//var _ io.Writer = &Collection{}

func NewCollection() *Collection {
	return &Collection{
		Broadcast:  make(chan Message),
		register:   make(chan *Socket),
		unregister: make(chan *Socket),
		close:      make(chan bool),
		sockets:    make(websocketMap),
	}
}

func (c *Collection) Upgrade(w http.ResponseWriter, r *http.Request) {
	socket, err := wsupgrader.Upgrade(w, r, nil)
	if err != nil {
		log.WithError(err).Error("Failed to upgrade to websocket")
		return
	}
	s := &Socket{
		collection: c,
		conn:       socket,
		send:       make(chan []byte, 256),
	}
	c.register <- s

	go s.readPump()
	go s.writePump()
}

func (c *Collection) Add(s *Socket) {
	c.register <- s
}

func (c *Collection) Remove(s *Socket) {
	c.unregister <- s
}

func (c *Collection) Run() {
	defer func() {
		for s := range c.sockets {
			close(s.send)
			delete(c.sockets, s)
		}
		close(c.register)
		close(c.unregister)
		close(c.Broadcast)
		close(c.close)
	}()
	for {
		select {
		case s := <-c.register:
			c.sockets[s] = true
		case s := <-c.unregister:
			if _, ok := c.sockets[s]; ok {
				delete(c.sockets, s)
				close(s.send)
			}
		case m := <-c.Broadcast:
			b, err := json.Marshal(m)
			if err != nil {
				log.WithError(err).Error("Failed to encode websocket message.")
				continue
			}
			for s := range c.sockets {
				select {
				case s.send <- b:
				default:
					close(s.send)
					delete(c.sockets, s)
				}
			}
		case <-c.close:
			return
		}
	}
}

func (c *Collection) Close() {
	c.close <- true
}
