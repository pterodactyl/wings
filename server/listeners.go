package server

import (
	"encoding/json"
	"github.com/apex/log"
	"github.com/pkg/errors"
	"github.com/pterodactyl/wings/api"
	"github.com/pterodactyl/wings/environment"
	"github.com/pterodactyl/wings/events"
	"regexp"
	"strconv"
)

// Adds all of the internal event listeners we want to use for a server.
func (s *Server) StartEventListeners() {
	console := make(chan events.Event)
	state := make(chan events.Event)
	stats := make(chan events.Event)

	go func(console chan events.Event) {
		for data := range console {
			// Immediately emit this event back over the server event stream since it is
			// being called from the environment event stream and things probably aren't
			// listening to that event.
			s.Events().Publish(ConsoleOutputEvent, data.Data)

			// Also pass the data along to the console output channel.
			s.onConsoleOutput(data.Data)
		}

		s.Log().Fatal("unexpected end-of-range for server console channel")
	}(console)

	go func(state chan events.Event) {
		for data := range state {
			s.SetState(data.Data)
		}

		s.Log().Fatal("unexpected end-of-range for server state channel")
	}(state)

	go func(stats chan events.Event) {
		for data := range stats {
			st := new(environment.Stats)
			if err := json.Unmarshal([]byte(data.Data), st); err != nil {
				s.Log().WithField("error", errors.WithStack(err)).Warn("failed to unmarshal server environment stats")
				continue
			}

			// Update the server resource tracking object with the resources we got here.
			s.resources.mu.Lock()
			s.resources.Stats = *st
			s.resources.mu.Unlock()

			s.Filesystem.HasSpaceAvailable(true)

			s.emitProcUsage()
		}

		s.Log().Fatal("unexpected end-of-range for server stats channel")
	}(stats)

	s.Log().Info("registering event listeners: console, state, resources...")
	s.Environment.Events().Subscribe([]string{environment.ConsoleOutputEvent}, console)
	s.Environment.Events().Subscribe([]string{environment.StateChangeEvent}, state)
	s.Environment.Events().Subscribe([]string{environment.ResourceEvent}, stats)
}

var stripAnsiRegex = regexp.MustCompile("[\u001B\u009B][[\\]()#;?]*(?:(?:(?:[a-zA-Z\\d]*(?:;[a-zA-Z\\d]*)*)?\u0007)|(?:(?:\\d{1,4}(?:;\\d{0,4})*)?[\\dA-PRZcf-ntqry=><~]))")

// Custom listener for console output events that will check if the given line
// of output matches one that should mark the server as started or not.
func (s *Server) onConsoleOutput(data string) {
	// Get the server's process configuration.
	processConfiguration := s.ProcessConfiguration()

	// Check if the server is currently starting.
	if s.GetState() == environment.ProcessStartingState {
		// Check if we should strip ansi color codes.
		if processConfiguration.Startup.StripAnsi {
			// Strip ansi color codes from the data string.
			data = stripAnsiRegex.ReplaceAllString(data, "")
		}

		// Iterate over all the done lines.
		for _, l := range processConfiguration.Startup.Done {
			if !l.Matches(data) {
				continue
			}

			s.Log().WithFields(log.Fields{
				"match":   l.String(),
				"against": strconv.QuoteToASCII(data),
			}).Debug("detected server in running state based on console line output")

			// If the specific line of output is one that would mark the server as started,
			// set the server to that state. Only do this if the server is not currently stopped
			// or stopping.
			_ = s.SetState(environment.ProcessRunningState)
			break
		}
	}

	// If the command sent to the server is one that should stop the server we will need to
	// set the server to be in a stopping state, otherwise crash detection will kick in and
	// cause the server to unexpectedly restart on the user.
	if s.IsRunning() {
		stop := processConfiguration.Stop

		if stop.Type == api.ProcessStopCommand && data == stop.Value {
			_ = s.SetState(environment.ProcessOfflineState)
		}
	}
}
