package server

import (
	"github.com/apex/log"
	"github.com/pterodactyl/wings/api"
	"strings"
)

// Adds all of the internal event listeners we want to use for a server.
func (s *Server) AddEventListeners() {
	consoleChannel := make(chan Event)
	s.Events().Subscribe(ConsoleOutputEvent, consoleChannel)

	go func() {
		for {
			select {
			case data := <-consoleChannel:
				s.onConsoleOutput(data.Data)
			}
		}
	}()
}

// Custom listener for console output events that will check if the given line
// of output matches one that should mark the server as started or not.
func (s *Server) onConsoleOutput(data string) {
	// If the specific line of output is one that would mark the server as started,
	// set the server to that state. Only do this if the server is not currently stopped
	// or stopping.
	match := s.ProcessConfiguration().Startup.Done

	if s.GetState() == ProcessStartingState && strings.Contains(data, match) {
		s.Log().WithFields(log.Fields{
			"match": match,
			"against": data,
		}).Debug("detected server in running state based on console line output")

		s.SetState(ProcessRunningState)
	}

	// If the command sent to the server is one that should stop the server we will need to
	// set the server to be in a stopping state, otherwise crash detection will kick in and
	// cause the server to unexpectedly restart on the user.
	if s.IsRunning() {
		stop := s.ProcessConfiguration().Stop
		if stop.Type == api.ProcessStopCommand && data == stop.Value {
			s.SetState(ProcessStoppingState)
		}
	}
}
