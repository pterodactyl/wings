package websockets

import "io"

type ConsoleWriter struct {
	Hub         *Hub
	HandlerFunc *func(string)
}

var _ io.Writer = ConsoleWriter{}

func (c ConsoleWriter) Write(b []byte) (n int, e error) {
	line := make([]byte, len(b))
	copy(line, b)
	m := Message{
		Type: MessageTypeConsole,
		Payload: ConsolePayload{
			Line:   string(line),
			Level:  ConsoleLevelPlain,
			Source: ConsoleSourceServer,
		},
	}
	c.Hub.Broadcast <- m
	if c.HandlerFunc != nil {
		(*c.HandlerFunc)(string(line))
	}
	return len(b), nil
}
