package server

import (
	"fmt"
	"github.com/mitchellh/colorstring"
	"io"
)

type Console struct {
	Server      *Server
	HandlerFunc *func(string)
}

var _ io.Writer = Console{}

func (c Console) Write(b []byte) (int, error) {
	if c.HandlerFunc != nil {
		l := make([]byte, len(b))
		copy(l, b)

		(*c.HandlerFunc)(string(l))
	}

	return len(b), nil
}

// Sends output to the server console formatted to appear correctly as being sent
// from Wings.
func (s *Server) PublishConsoleOutputFromDaemon(data string) {
	s.Events().Publish(
		ConsoleOutputEvent,
		colorstring.Color(fmt.Sprintf("[yellow][bold][Pterodactyl Daemon]:[default] %s", data)),
	)
}
