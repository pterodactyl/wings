package server

import (
	"fmt"
	"github.com/mitchellh/colorstring"
	"github.com/pterodactyl/wings/config"
	"sync"
	"sync/atomic"
	"time"
)

type ConsoleThrottler struct {
	sync.RWMutex
	config.ConsoleThrottles

	// The total number of activations that have occurred thus far.
	activations uint64

	// The total number of lines processed so far during the given time period.
	lines uint64

	lastIntervalTime *time.Time
	lastDecayTime    *time.Time
}

// Increments the number of activations for a server.
func (ct *ConsoleThrottler) AddActivation() uint64 {
	ct.Lock()
	defer ct.Unlock()

	ct.activations += 1

	return ct.activations
}

// Decrements the number of activations for a server.
func (ct *ConsoleThrottler) RemoveActivation() uint64 {
	ct.Lock()
	defer ct.Unlock()

	if ct.activations == 0 {
		return 0
	}

	ct.activations -= 1

	return ct.activations
}

// Increment the total count of lines that we have processed so far.
func (ct *ConsoleThrottler) IncrementLineCount() uint64 {
	return atomic.AddUint64(&ct.lines, 1)
}

// Reset the line count to zero.
func (ct *ConsoleThrottler) ResetLineCount() {
	atomic.SwapUint64(&ct.lines, 0)
}

// Handles output from a server's console. This code ensures that a server is not outputting
// an excessive amount of data to the console that could indicate a malicious or run-away process
// and lead to performance issues for other users.
//
// This was much more of a problem for the NodeJS version of the daemon which struggled to handle
// large volumes of output. However, this code is much more performant so I generally feel a lot
// better about it's abilities.
//
// However, extreme output is still somewhat of a DoS attack vector against this software since we
// are still logging it to the disk temporarily and will want to avoid dumping a huge amount of
// data all at once. These values are all configurable via the wings configuration file, however the
// defaults have been in the wild for almost two years at the time of this writing, so I feel quite
// confident in them.
func (ct *ConsoleThrottler) Handle() {

}

// Returns the throttler instance for the server or creates a new one.
func (s *Server) Throttler() *ConsoleThrottler {
	s.throttleLock.RLock()

	if s.throttler == nil {
		// Release the read lock so that we can acquire a normal lock on the process and
		// make modifications to the throttler.
		s.throttleLock.RUnlock()

		s.throttleLock.Lock()
		s.throttler = &ConsoleThrottler{
			ConsoleThrottles: config.Get().Throttles,
		}
		s.throttleLock.Unlock()

		return s.throttler
	} else {
		defer s.throttleLock.RUnlock()
		return s.throttler
	}
}

// Sends output to the server console formatted to appear correctly as being sent
// from Wings.
func (s *Server) PublishConsoleOutputFromDaemon(data string) {
	s.Events().Publish(
		ConsoleOutputEvent,
		colorstring.Color(fmt.Sprintf("[yellow][bold][Pterodactyl Daemon]:[default] %s", data)),
	)
}
