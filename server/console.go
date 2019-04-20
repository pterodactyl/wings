package server

import "io"

type Console struct {
	Server *Server
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