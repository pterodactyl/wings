package server

import (
	"fmt"
	"github.com/mitchellh/colorstring"
	"github.com/pkg/errors"
	"github.com/pterodactyl/wings/config"
	"sync"
	"sync/atomic"
	"time"
)

var ErrTooMuchConsoleData = errors.New("console is outputting too much data")

type ConsoleThrottler struct {
	sync.RWMutex
	config.ConsoleThrottles

	// The total number of activations that have occurred thus far.
	activations uint64
	triggered   bool

	// The total number of lines processed so far during the given time period.
	count           uint64
	activationTimer *time.Time
	outputTimer     *time.Time
}

// Resets the state of the throttler.
func (ct *ConsoleThrottler) Reset() {
	ct.Lock()
	ct.count = 0
	ct.activations = 0
	ct.triggered = false
	ct.outputTimer = nil
	ct.activationTimer = nil
	ct.Unlock()
}

// Triggers an activation for a server. You can also decrement the number of activations
// by passing a negative number.
func (ct *ConsoleThrottler) Trigger(count int64) uint64 {
	if (int64(ct.activations) + count) <= 0 {
		ct.activations = 0
	} else {
		ct.activations = uint64(int64(ct.activations) + count)
	}

	n := time.Now()
	if count > 0 {
		ct.activationTimer = &n
	}

	return ct.activations
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
//
// This function returns an error if the server should be stopped due to violating throttle constraints
// and a boolean value indicating if a throttle is being violated when it is checked.
func (ct *ConsoleThrottler) Check(onTrigger func()) (bool, error) {
	if !ct.Enabled {
		return false, nil
	}

	ct.Lock()
	defer ct.Unlock()

	n := time.Now()
	// Check if last time that an output throttle activation occurred, and if it has been enough time,
	// go ahead and decrement the count.
	if ct.activationTimer != nil && time.Now().After(ct.activationTimer.Add(time.Duration(ct.Decay)*time.Millisecond)) {
		ct.Trigger(-1)
	}

	// Check if the last decay time is too old, and if so reset the count of the lines that have
	// been processed by this instance.
	if ct.outputTimer != nil && time.Now().After(ct.outputTimer.Add(time.Duration(ct.CheckInterval)*time.Millisecond)) {
		ct.outputTimer = &n
		ct.triggered = false
		atomic.SwapUint64(&ct.count, 0)
	} else if ct.outputTimer == nil {
		ct.outputTimer = &n
	}

	// Increment the line count and if we have now output more lines than are allowed, trigger a throttle
	// activation. Once the throttle is triggered and has passed the kill at value we will trigger a server
	// stop automatically.
	if atomic.AddUint64(&ct.count, 1) >= ct.Lines && !ct.triggered {
		ct.triggered = true
		if ct.Trigger(1) >= ct.KillAtCount {
			return true, ErrTooMuchConsoleData
		}

		onTrigger()
	}

	// Making it here means there is nothing else that needs to be done to the server.
	return ct.triggered, nil
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
