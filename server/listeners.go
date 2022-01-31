package server

import (
	"bytes"
	"regexp"
	"strconv"
	"sync"

	"github.com/apex/log"

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

// processConsoleOutputEvent handles output from a server's Docker container
// and runs through different limiting logic to ensure that spam console output
// does not cause negative effects to the system. This will also monitor the
// output lines to determine if the server is started yet, and if the output is
// not being throttled, will send the data over to the websocket.
func (s *Server) processConsoleOutputEvent(v []byte) {
	// Always process the console output, but do this in a seperate thread since we
	// don't really care about side-effects from this call, and don't want it to block
	// the console sending logic.
	go s.onConsoleOutput(v)

	// If the console is being throttled, do nothing else with it, we don't want
	// to waste time. This code previously terminated server instances after violating
	// different throttle limits. That code was clunky and difficult to reason about,
	// in addition to being a consistent pain point for users.
	//
	// In the interest of building highly efficient software, that code has been removed
	// here, and we'll rely on the host to detect bad actors through their own means.
	if !s.Throttler().Allow() {
		return
	}

	s.Sink(LogSink).Push(v)
}

// StartEventListeners adds all the internal event listeners we want to use for
// a server. These listeners can only be removed by deleting the server as they
// should last for the duration of the process' lifetime.
func (s *Server) StartEventListeners() {
	state := make(chan events.Event)
	stats := make(chan events.Event)
	docker := make(chan events.Event)

	go func() {
		l := newDiskLimiter(s)

		for {
			select {
			case e := <-state:
				go func() {
					// Reset the throttler when the process is started.
					if e.Data == environment.ProcessStartingState {
						l.Reset()
						s.Throttler().Reset()
					}

					s.OnStateChange()
				}()
			case e := <-stats:
				go func() {
					s.resources.UpdateStats(e.Data.(environment.Stats))

					// If there is no disk space available at this point, trigger the server
					// disk limiter logic which will start to stop the running instance.
					if !s.Filesystem().HasSpaceAvailable(true) {
						l.Trigger()
					}

					s.Events().Publish(StatsEvent, s.Proc())
				}()
			case e := <-docker:
				go func() {
					switch e.Topic {
					case environment.DockerImagePullStatus:
						s.Events().Publish(InstallOutputEvent, e.Data)
					case environment.DockerImagePullStarted:
						s.PublishConsoleOutputFromDaemon("Pulling Docker container image, this could take a few minutes to complete...")
					case environment.DockerImagePullCompleted:
						s.PublishConsoleOutputFromDaemon("Finished pulling Docker container image")
					default:
						s.Log().WithField("topic", e.Topic).Error("unhandled docker event topic")
					}
				}()
			}
		}
	}()

	s.Log().Debug("registering event listeners: console, state, resources...")
	s.Environment.SetLogCallback(s.processConsoleOutputEvent)
	s.Environment.Events().On(state, environment.StateChangeEvent)
	s.Environment.Events().On(stats, environment.ResourceEvent)
	s.Environment.Events().On(docker, dockerEvents...)
}

var stripAnsiRegex = regexp.MustCompile("[\u001B\u009B][[\\]()#;?]*(?:(?:(?:[a-zA-Z\\d]*(?:;[a-zA-Z\\d]*)*)?\u0007)|(?:(?:\\d{1,4}(?:;\\d{0,4})*)?[\\dA-PRZcf-ntqry=><~]))")

// Custom listener for console output events that will check if the given line
// of output matches one that should mark the server as started or not.
func (s *Server) onConsoleOutput(data []byte) {
	if s.Environment.State() != environment.ProcessStartingState && !s.IsRunning() {
		return
	}

	processConfiguration := s.ProcessConfiguration()

	// Make a copy of the data provided since it is by reference, otherwise you'll
	// potentially introduce a race condition by modifying the value.
	v := make([]byte, len(data))
	copy(v, data)

	// Check if the server is currently starting.
	if s.Environment.State() == environment.ProcessStartingState {
		// Check if we should strip ansi color codes.
		if processConfiguration.Startup.StripAnsi {
			v = stripAnsiRegex.ReplaceAll(v, []byte(""))
		}

		// Iterate over all the done lines.
		for _, l := range processConfiguration.Startup.Done {
			if !l.Matches(v) {
				continue
			}

			s.Log().WithFields(log.Fields{
				"match":   l.String(),
				"against": strconv.QuoteToASCII(string(v)),
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

		if stop.Type == remote.ProcessStopCommand && bytes.Equal(v, []byte(stop.Value)) {
			s.Environment.SetState(environment.ProcessOfflineState)
		}
	}
}
