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
	// Disallow start & restart if the server is suspended.
	if action.IsStart() && s.IsSuspended() {
		return new(suspendedError)
	}

	if s.powerLock == nil {
		s.powerLock = semaphore.NewWeighted(1)
	}

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

	if action.IsStart() {
		s.Log().Info("syncing server configuration with panel")
		if err := s.Sync(); err != nil {
			return errors.WithStack(err)
		}

		if !s.Filesystem.HasSpaceAvailable() {
			return errors.New("cannot start server, not enough disk space available")
		}
	}

	switch action {
	case PowerActionStart:
		return s.Environment.Start()
	case PowerActionStop:
		return s.Environment.Stop()
	case PowerActionRestart:
		return s.Environment.Restart()
	case PowerActionTerminate:
		return s.Environment.Terminate(os.Kill)
	}

	return errors.New("attempting to handle unknown power action")
}
