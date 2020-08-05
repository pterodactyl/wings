package server

import (
	"github.com/apex/log"
	"github.com/pterodactyl/wings/api"
	"regexp"
	"strings"
	"sync"
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

var (
	stripAnsiRegex = regexp.MustCompile("[\u001B\u009B][[\\]()#;?]*(?:(?:(?:[a-zA-Z\\d]*(?:;[a-zA-Z\\d]*)*)?\u0007)|(?:(?:\\d{1,4}(?:;\\d{0,4})*)?[\\dA-PRZcf-ntqry=><~]))")

	regexpCacheMx sync.RWMutex
	regexpCache   map[string]*regexp.Regexp
)

// Custom listener for console output events that will check if the given line
// of output matches one that should mark the server as started or not.
func (s *Server) onConsoleOutput(data string) {
	// Get the server's process configuration.
	processConfiguration := s.ProcessConfiguration()

	// Check if the server is currently starting.
	if s.GetState() == ProcessStartingState {
		// If the specific line of output is one that would mark the server as started,
		// set the server to that state. Only do this if the server is not currently stopped
		// or stopping.

		// Check if we should strip ansi color codes.
		if processConfiguration.Startup.StripAnsi {
			// Strip ansi color codes from the data string.
			data = stripAnsiRegex.ReplaceAllString(data, "")
		}

		// Iterate over all the done lines.
		for _, match := range processConfiguration.Startup.Done {
			if strings.HasPrefix(match, "regex:") && len(match) > 6 {
				match = match[6:]

				regexpCacheMx.RLock()
				rxp, ok := regexpCache[match]
				regexpCacheMx.RUnlock()

				if !ok {
					var err error

					rxp, err = regexp.Compile(match)
					if err != nil {
						log.WithError(err).Warn("failed to compile regexp")
						break
					}

					regexpCacheMx.Lock()
					regexpCache[match] = rxp
					regexpCacheMx.Unlock()
				}

				if !rxp.MatchString(data) {
					continue
				}
			} else if !strings.Contains(data, match) {
				continue
			}

			s.Log().WithFields(log.Fields{
				"match":   match,
				"against": data,
			}).Debug("detected server in running state based on console line output")

			_ = s.SetState(ProcessRunningState)
			break
		}
	}

	// If the command sent to the server is one that should stop the server we will need to
	// set the server to be in a stopping state, otherwise crash detection will kick in and
	// cause the server to unexpectedly restart on the user.
	if s.IsRunning() {
		stop := processConfiguration.Stop

		if stop.Type == api.ProcessStopCommand && data == stop.Value {
			_ = s.SetState(ProcessStoppingState)
		}
	}
}
