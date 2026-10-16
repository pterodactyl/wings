package server

import (
	"context"
	"testing"
	"time"

	"emperror.dev/errors"
	"github.com/stretchr/testify/require"

	"github.com/pterodactyl/wings/environment"
	"github.com/pterodactyl/wings/system"
)

// bootTestEnvironment reports a fixed process state and records kill signals.
type bootTestEnvironment struct {
	environment.ProcessEnvironment
	state      string
	terminated chan struct{}
}

func (e *bootTestEnvironment) State() string { return e.state }

func (e *bootTestEnvironment) Terminate(context.Context, string) error {
	close(e.terminated)
	return nil
}

func newBootTestServer(t *testing.T, state string) (*Server, *bootTestEnvironment) {
	s, err := New(nil)
	require.NoError(t, err)
	t.Cleanup(s.CtxCancel)
	env := &bootTestEnvironment{state: state, terminated: make(chan struct{})}
	s.Environment = env
	return s, env
}

// A kill sent while a boot holds the power lock runs once the boot releases it.
func TestKillWhileBootingWaitsForBoot(t *testing.T) {
	s, env := newBootTestServer(t, environment.ProcessStartingState)
	require.NoError(t, s.powerLock.Acquire())

	done := make(chan error, 1)
	go func() { done <- s.HandlePowerAction(PowerActionTerminate, 5) }()

	select {
	case <-env.terminated:
		t.Fatal("the server was killed before the boot released the power lock")
	case <-time.After(100 * time.Millisecond):
	}

	s.powerLock.Release()

	select {
	case <-env.terminated:
	case <-time.After(2 * time.Second):
		t.Fatal("the server was not killed after the boot released the power lock")
	}
	require.NoError(t, <-done)
	require.False(t, s.ExecutingPowerAction())
}

// A kill sent while a boot holds the power lock, without a wait, reports the lock
// instead of marking the server offline.
func TestKillWhileBootingWithoutWaitReturnsLockError(t *testing.T) {
	s, env := newBootTestServer(t, environment.ProcessOfflineState)
	require.NoError(t, s.powerLock.Acquire())

	err := s.HandlePowerAction(PowerActionTerminate)
	require.True(t, errors.Is(err, system.ErrLockerLocked), "got %v", err)

	select {
	case <-env.terminated:
		t.Fatal("the server was killed while it was booting")
	default:
	}
}

// A kill still passes through the power lock held by a stop, so a server that is
// stuck stopping can be killed.
func TestKillWhileStoppingSkipsLock(t *testing.T) {
	s, env := newBootTestServer(t, environment.ProcessStoppingState)
	require.NoError(t, s.powerLock.Acquire())

	require.NoError(t, s.HandlePowerAction(PowerActionTerminate, 5))

	select {
	case <-env.terminated:
	default:
		t.Fatal("the server was not killed while stopping")
	}
	require.True(t, s.ExecutingPowerAction())
}
