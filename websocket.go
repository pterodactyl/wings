package main

import (
	"fmt"
	"github.com/googollee/go-socket.io"
	"go.uber.org/zap"
)

// Configures the websocket connection and attaches it to the Router struct.
func (rt *Router) ConfigureWebsocket() (*socketio.Server, error) {
	s, err := socketio.NewServer(nil)

	if err != nil {
		return nil, err
	}

	s.OnConnect("/", func(s socketio.Conn) error {
		s.SetContext("")
		fmt.Println("connected:", s.ID())
		return nil
	})

	s.OnError("/", func(e error) {
		zap.S().Error(e)
	})

	return s, nil
}