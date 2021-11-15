package server

import (
	"encoding/json"
	"regexp"
	"strconv"
	"sync"

	"github.com/apex/log"

	"github.com/pterodactyl/wings/config"
	"github.com/pterodactyl/wings/environment"
	"github.com/pterodactyl/wings/events"
	"github.com/pterodactyl/wings/remote"
)

var dockerEvents = []string{
	environment.DockerImagePullStatus,
	environment.DockerImagePullStarted,
	environment.DockerImagePullCompleted,
}

type diskSpaceLimiter struct {
	o      sync.Once
	mu     sync.Mutex
	server *Server
}

func newDiskLimiter(s *Server) *diskSpaceLimiter {
	return &diskSpaceLimiter{server: s}
}

// Reset the disk space limiter status.
func (dsl *diskSpaceLimiter) Reset() {
	dsl.mu.Lock()
	dsl.o = sync.Once{}
	dsl.mu.Unlock()
}

// Trigger the disk space limiter which will attempt to stop a running server instance within
// 15 seconds, and terminate it forcefully if it does not stop.
//
// This function is only executed one time, so whenever a server is marked as booting the limiter
// should be reset, so it can properly be triggered as needed.
func (dsl *diskSpaceLimiter) Trigger() {
	dsl.o.Do(func() {
		dsl.server.PublishConsoleOutputFromDaemon("Server is exceeding the assigned disk space limit, stopping process now.")
		if err := dsl.server.Environment.WaitForStop(60, true); err != nil {
			dsl.server.Log().WithField("error", err).Error("failed to stop server after exceeding space limit!")
		}
	})
}

// StartEventListeners adds all the internal event listeners we want to use for a server. These listeners can only be
// removed by deleting the server as they should last for the duration of the process' lifetime.
func (s *Server) StartEventListeners() {
	console := func(e events.Event) {
		t := s.Throttler()
		err := t.Increment(func() {
			s.PublishConsoleOutputFromDaemon("Your server is outputting too much data and is being throttled.")
		})
		// An error is only returned if the server has breached the thresholds set.
		if err != nil {
			// If the process is already stopping, just let it continue with that action rather than attempting
			// to terminate again.
			if s.Environment.State() != environment.ProcessStoppingState {
				s.Environment.SetState(environment.ProcessStoppingState)

				go func() {
					s.Log().Warn("stopping server instance, violating throttle limits")
					s.PublishConsoleOutputFromDaemon("Your server is being stopped for outputting too much data in a short period of time.")

					// Completely skip over server power actions and terminate the running instance. This gives the
					// server 15 seconds to finish stopping gracefully before it is forcefully terminated.
					if err := s.Environment.WaitForStop(config.Get().Throttles.StopGracePeriod, true); err != nil {
						// If there is an error set the process back to running so that this throttler is called
						// again and hopefully kills the server.
						if s.Environment.State() != environment.ProcessOfflineState {
							s.Environment.SetState(environment.ProcessRunningState)
						}

						s.Log().WithField("error", err).Error("failed to terminate environment after triggering throttle")
					}
				}()
			}
		}

		// If we are not throttled, go ahead and output the data.
		if !t.Throttled() {
			s.Events().Publish(ConsoleOutputEvent, e.Data)
		}

		// Also pass the data along to the console output channel.
		s.onConsoleOutput(e.Data)
	}

	l := newDiskLimiter(s)
	state := func(e events.Event) {
		// Reset the throttler when the process is started.
		if e.Data == environment.ProcessStartingState {
			l.Reset()
			s.Throttler().Reset()
		}

		s.OnStateChange()
	}

	stats := func(e events.Event) {
		var st environment.Stats
		if err := json.Unmarshal([]byte(e.Data), &st); err != nil {
			s.Log().WithField("error", err).Warn("failed to unmarshal server environment stats")
			return
		}

		// Update the server resource tracking object with the resources we got here.
		s.resources.mu.Lock()
		s.resources.Stats = st
		s.resources.mu.Unlock()

		// If there is no disk space available at this point, trigger the server disk limiter logic
		// which will start to stop the running instance.
		if !s.Filesystem().HasSpaceAvailable(true) {
			l.Trigger()
		}

		s.emitProcUsage()
	}

	docker := func(e events.Event) {
		if e.Topic == environment.DockerImagePullStatus {
			s.Events().Publish(InstallOutputEvent, e.Data)
		} else if e.Topic == environment.DockerImagePullStarted {
			s.PublishConsoleOutputFromDaemon("Pulling Docker container image, this could take a few minutes to complete...")
		} else {
			s.PublishConsoleOutputFromDaemon("Finished pulling Docker container image")
		}
	}

	s.Log().Debug("registering event listeners: console, state, resources...")
	s.Environment.Events().On(environment.ConsoleOutputEvent, &console)
	s.Environment.Events().On(environment.StateChangeEvent, &state)
	s.Environment.Events().On(environment.ResourceEvent, &stats)
	for _, evt := range dockerEvents {
		s.Environment.Events().On(evt, &docker)
	}
}

var stripAnsiRegex = regexp.MustCompile("[\u001B\u009B][[\\]()#;?]*(?:(?:(?:[a-zA-Z\\d]*(?:;[a-zA-Z\\d]*)*)?\u0007)|(?:(?:\\d{1,4}(?:;\\d{0,4})*)?[\\dA-PRZcf-ntqry=><~]))")

// Custom listener for console output events that will check if the given line
// of output matches one that should mark the server as started or not.
func (s *Server) onConsoleOutput(data string) {
	// Get the server's process configuration.
	processConfiguration := s.ProcessConfiguration()

	// Check if the server is currently starting.
	if s.Environment.State() == environment.ProcessStartingState {
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
			s.Environment.SetState(environment.ProcessRunningState)
			break
		}
	}

	// If the command sent to the server is one that should stop the server we will need to
	// set the server to be in a stopping state, otherwise crash detection will kick in and
	// cause the server to unexpectedly restart on the user.
	if s.IsRunning() {
		stop := processConfiguration.Stop

		if stop.Type == remote.ProcessStopCommand && data == stop.Value {
			s.Environment.SetState(environment.ProcessOfflineState)
		}
	}
}
