package server

import (
	"go.uber.org/zap"
	"strings"
)


// Adds all of the internal event listeners we want to use for a server.
func (s *Server) AddEventListeners() {
	s.AddListener(ConsoleOutputEvent, s.onConsoleOutput())
}

var onConsoleOutputListener func(string)

// Custom listener for console output events that will check if the given line
// of output matches one that should mark the server as started or not.
func (s *Server) onConsoleOutput() *func(string) {
	if onConsoleOutputListener == nil {
		onConsoleOutputListener = func (data string) {
			// If the specific line of output is one that would mark the server as started,
			// set the server to that state. Only do this if the server is not currently stopped
			// or stopping.
			if s.State == ProcessStartingState && strings.Contains(data, s.processConfiguration.Startup.Done) {
				zap.S().Debugw(
					"detected server in running state based on line output", zap.String("match", s.processConfiguration.Startup.Done), zap.String("against", data),
				)

				s.SetState(ProcessRunningState)
			}
		}
	}

	return &onConsoleOutputListener
}