package server

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"emperror.dev/errors"
	"github.com/mitchellh/colorstring"

	"github.com/pterodactyl/wings/config"
	"github.com/pterodactyl/wings/system"
)

// appName is a local cache variable to avoid having to make expensive copies of
// the configuration every time we need to send output along to the websocket for
// a server.
var appName string

var appNameSync sync.Once

var ErrTooMuchConsoleData = errors.New("console is outputting too much data")

type ConsoleThrottler struct {
	mu sync.Mutex
	config.ConsoleThrottles

	// The total number of activations that have occurred thus far.
	activations uint64

	// The total number of lines that have been sent since the last reset timer period.
	count uint64

	// Wether or not the console output is being throttled. It is up to calling code to
	// determine what to do if it is.
	isThrottled *system.AtomicBool

	// The total number of lines processed so far during the given time period.
	timerCancel *context.CancelFunc
}

// Resets the state of the throttler.
func (ct *ConsoleThrottler) Reset() {
	atomic.StoreUint64(&ct.count, 0)
	atomic.StoreUint64(&ct.activations, 0)
	ct.isThrottled.Store(false)
}

// Triggers an activation for a server. You can also decrement the number of activations
// by passing a negative number.
func (ct *ConsoleThrottler) markActivation(increment bool) uint64 {
	if !increment {
		if atomic.LoadUint64(&ct.activations) == 0 {
			return 0
		}

		// This weird dohicky subtracts 1 from the activation count.
		return atomic.AddUint64(&ct.activations, ^uint64(0))
	}

	return atomic.AddUint64(&ct.activations, 1)
}

// Determines if the console is currently being throttled. Calls to this function can be used to
// determine if output should be funneled along to the websocket processes.
func (ct *ConsoleThrottler) Throttled() bool {
	return ct.isThrottled.Load()
}

// Starts a timer that runs in a seperate thread and will continually decrement the lines processed
// and number of activations, regardless of the current console message volume. All of the timers
// are canceled if the context passed through is canceled.
func (ct *ConsoleThrottler) StartTimer(ctx context.Context) {
	system.Every(ctx, time.Duration(int64(ct.LineResetInterval))*time.Millisecond, func(_ time.Time) {
		ct.isThrottled.Store(false)
		atomic.StoreUint64(&ct.count, 0)
	})

	system.Every(ctx, time.Duration(int64(ct.DecayInterval))*time.Millisecond, func(_ time.Time) {
		ct.markActivation(false)
	})
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
func (ct *ConsoleThrottler) Increment(onTrigger func()) error {
	if !ct.Enabled {
		return nil
	}

	// Increment the line count and if we have now output more lines than are allowed, trigger a throttle
	// activation. Once the throttle is triggered and has passed the kill at value we will trigger a server
	// stop automatically.
	if atomic.AddUint64(&ct.count, 1) >= ct.Lines && !ct.Throttled() {
		ct.isThrottled.Store(true)
		if ct.markActivation(true) >= ct.MaximumTriggerCount {
			return ErrTooMuchConsoleData
		}

		onTrigger()
	}

	return nil
}

// Returns the throttler instance for the server or creates a new one.
func (s *Server) Throttler() *ConsoleThrottler {
	s.throttleOnce.Do(func() {
		s.throttler = &ConsoleThrottler{
			isThrottled:      system.NewAtomicBool(false),
			ConsoleThrottles: config.Get().Throttles,
		}
	})
	return s.throttler
}

// PublishConsoleOutputFromDaemon sends output to the server console formatted
// to appear correctly as being sent from Wings.
func (s *Server) PublishConsoleOutputFromDaemon(data string) {
	appNameSync.Do(func() {
		appName = config.Get().AppName
	})
	s.Events().Publish(
		ConsoleOutputEvent,
		colorstring.Color(fmt.Sprintf("[yellow][bold][%s Daemon]:[default] %s", appName, data)),
	)
}
