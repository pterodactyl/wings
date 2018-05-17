package control

import (
	"io"

	"github.com/pterodactyl/wings/api/websockets"
)

type ConsoleHandler struct {
	Websockets  *websockets.Collection
	HandlerFunc *func(string)
}

var _ io.Writer = ConsoleHandler{}

func (c ConsoleHandler) Write(b []byte) (n int, e error) {
	l := make([]byte, len(b))
	copy(l, b)
	line := string(l)
	m := websockets.Message{
		Type: websockets.MessageTypeConsole,
		Payload: websockets.ConsolePayload{
			Line:   line,
			Level:  websockets.ConsoleLevelPlain,
			Source: websockets.ConsoleSourceServer,
		},
	}
	c.Websockets.Broadcast <- m
	if c.HandlerFunc != nil {
		(*c.HandlerFunc)(line)
	}
	return len(b), nil
}
