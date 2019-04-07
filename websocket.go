package main

import (
	"github.com/googollee/go-engine.io"
	"github.com/googollee/go-engine.io/transport"
	"github.com/googollee/go-engine.io/transport/websocket"
	"github.com/googollee/go-socket.io"
	"github.com/julienschmidt/httprouter"
	"go.uber.org/zap"
	"net/http"
)

// Configures the websocket connection and attaches it to the Router struct.
func (rt *Router) ConfigureWebsocket() (*socketio.Server, error) {
	s, err := socketio.NewServer(&engineio.Options{
		Transports: []transport.Transport{
			websocket.Default,
		},
	})

	if err != nil {
		return nil, err
	}

	s.OnError("/", func(e error) {
		zap.S().Error(e)
	})

	return s, nil
}

func (rt *Router) routeWebsocket(w http.ResponseWriter, r *http.Request, ps httprouter.Params) {
	server := rt.Servers.Get(ps.ByName("server"))

	rt.Socketio.OnConnect("/", func (s socketio.Conn) error {
		s.SetContext("")

		zap.S().Infof("connected to socket for server: %s", server.Uuid)

		s.Emit("initial status", server.State)

		return nil
	})

	rt.Socketio.OnEvent("/", "status", func(s socketio.Conn, msg string) string {
		s.Emit("reply", "thanks: " + msg)

		return "recv: " + msg
	})

	rt.Socketio.ServeHTTP(w, r)
}
