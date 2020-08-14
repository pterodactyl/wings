package server

import (
	"context"
	"github.com/pkg/errors"
	"golang.org/x/sync/semaphore"
	"os"
	"time"
)

type PowerAction string

// The power actions that can be performed for a given server. This taps into the given server
// environment and performs them in a way that prevents a race condition from occurring. For
// example, sending two "start" actions back to back will not process the second action until
// the first action has been completed.
//
// This utilizes a workerpool with a limit of one worker so that all of the actions execute
// in a sync manner.
const (
	PowerActionStart     = "start"
	PowerActionStop      = "stop"
	PowerActionRestart   = "restart"
	PowerActionTerminate = "kill"
)

// Checks if the power action being received is valid.
func (pa PowerAction) IsValid() bool {
	return pa == PowerActionStart ||
		pa == PowerActionStop ||
		pa == PowerActionTerminate ||
		pa == PowerActionRestart
}

func (pa PowerAction) IsStart() bool {
	return pa == PowerActionStart || pa == PowerActionRestart
}

// Helper function that can receive a power action and then process the actions that need
// to occur for it. This guards against someone calling Start() twice at the same time, or
// trying to restart while another restart process is currently running.
//
// However, the code design for the daemon does depend on the user correctly calling this
// function rather than making direct calls to the start/stop/restart functions on the
// environment struct.
func (s *Server) HandlePowerAction(action PowerAction, waitSeconds ...int) error {
	if s.powerLock == nil {
		s.powerLock = semaphore.NewWeighted(1)
	}

	// Only attempt to acquire a lock on the process if this is not a termination event. We want to
	// just allow those events to pass right through for good reason. If a server is currently trying
	// to process a power action but has gotten stuck you still should be able to pass through the
	// terminate event. The good news here is that doing that oftentimes will get the stuck process to
	// move again, and naturally continue through the process.
	if action != PowerActionTerminate {
		// Determines if we should wait for the lock or not. If a value greater than 0 is passed
		// into this function we will wait that long for a lock to be acquired.
		if len(waitSeconds) > 0 && waitSeconds[0] != 0 {
			ctx, _ := context.WithTimeout(context.Background(), time.Second*time.Duration(waitSeconds[0]))
			// Attempt to acquire a lock on the power action lock for up to 30 seconds. If more
			// time than that passes an error will be propagated back up the chain and this
			// request will be aborted.
			if err := s.powerLock.Acquire(ctx, 1); err != nil {
				return errors.WithMessage(err, "could not acquire lock on power state")
			}
		} else {
			// If no wait duration was provided we will attempt to immediately acquire the lock
			// and bail out with a context deadline error if it is not acquired immediately.
			if ok := s.powerLock.TryAcquire(1); !ok {
				return errors.WithMessage(context.DeadlineExceeded, "could not acquire lock on power state")
			}
		}

		// Release the lock once the process being requested has finished executing.
		defer s.powerLock.Release(1)
	} else {
		// Still try to acquire the lock if terminating and it is available, just so that other power
		// actions are blocked until it has completed. However, if it is unavailable we won't stop
		// the entire process.
		if ok := s.powerLock.TryAcquire(1); ok {
			// If we managed to acquire the lock be sure to released it once this process is completed.
			defer s.powerLock.Release(1)
		}
	}

	switch action {
	case PowerActionStart:
		// Run the pre-boot logic for the server before processing the environment start.
		if err := s.onBeforeStart(); err != nil {
			return err
		}

		return s.Environment.Start()
	case PowerActionStop:
		// We're specificially waiting for the process to be stopped here, otherwise the lock is released
		// too soon, and you can rack up all sorts of issues.
		return s.Environment.WaitForStop(10 * 60, true)
	case PowerActionRestart:
		// Only try to wait for stop if the process is currently running, otherwise just skip right to the
		// start event.
		if r, _ := s.Environment.IsRunning(); !r {
			if err := s.Environment.WaitForStop(10 * 60, true); err != nil {
				// Even timeout errors should be bubbled back up the stack. If the process didn't stop
				// nicely, but the terminate argument was passed then the server is stopped without an
				// error being returned.
				//
				// However, if terminate is not passed you'll get a context deadline error. We could
				// probably handle that nicely here, but I'd rather just pass it back up the stack for now.
				// Either way, any type of error indicates we should not attempt to start the server back
				// up.
				return err
			}
		}

		// Now actually try to start the process by executing the normal pre-boot logic.
		if err := s.onBeforeStart(); err != nil {
			return err
		}

		return s.Environment.Start()
	case PowerActionTerminate:
		return s.Environment.Terminate(os.Kill)
	}

	return errors.New("attempting to handle unknown power action")
}

// Execute a few functions before actually calling the environment start commands. This ensures
// that everything is ready to go for environment booting, and that the server can even be started.
func (s *Server) onBeforeStart() error {
	// Disallow start & restart if the server is suspended.
	if s.IsSuspended() {
		return new(suspendedError)
	}

	s.Log().Info("syncing server configuration with panel")
	if err := s.Sync(); err != nil {
		return errors.WithMessage(err, "unable to sync server data from Panel instance")
	}

	if !s.Filesystem.HasSpaceAvailable() {
		return errors.New("cannot start server, not enough disk space available")
	}

	s.UpdateConfigurationFiles()

	return nil
}