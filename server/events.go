package server

import (
	"fmt"
	"github.com/mitchellh/colorstring"
)

type EventListeners map[string][]EventListenerFunction

type EventListenerFunction *func(string)

// Defines all of the possible output events for a server.
// noinspection GoNameStartsWithPackageName
const (
	DaemonMessageEvent = "daemon message"
	InstallOutputEvent = "install output"
	ConsoleOutputEvent = "console output"
	StatusEvent        = "status"
	StatsEvent         = "stats"
)

// Adds an event listener for the server instance.
func (s *Server) AddListener(event string, f EventListenerFunction) {
	if s.listeners == nil {
		s.listeners = make(map[string][]EventListenerFunction)
	}

	if _, ok := s.listeners[event]; ok {
		s.listeners[event] = append(s.listeners[event], f)
	} else {
		s.listeners[event] = []EventListenerFunction{f}
	}
}

// Removes the event listener for the server instance.
func (s *Server) RemoveListener(event string, f EventListenerFunction) {
	if _, ok := s.listeners[event]; ok {
		for i := range s.listeners[event] {
			if s.listeners[event][i] == f {
				s.listeners[event] = append(s.listeners[event][:i], s.listeners[event][i+1:]...)
				break
			}
		}
	}
}

// Emits an event to all of the active listeners for a server.
func (s *Server) Emit(event string, data string) {
	if _, ok := s.listeners[event]; ok {
		for _, handler := range s.listeners[event] {
			go func(f EventListenerFunction, d string) {
				(*f)(d)
			}(handler, data)
		}
	}
}

// Sends output to the server console formatted to appear correctly as being sent
// from Wings.
func (s *Server) SendConsoleOutputFromDaemon(data string) {
	s.Emit(
		ConsoleOutputEvent,
		colorstring.Color(fmt.Sprintf("[yellow][bold][Pterodactyl Daemon]:[default] %s", data)),
	)
}
