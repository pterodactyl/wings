package system

import (
	"context"
	"testing"
	"time"

	"emperror.dev/errors"
	"github.com/stretchr/testify/require"
)

func TestLockerCannotBeAcquiredAfterDestroy(t *testing.T) {
	l := NewLocker()
	l.Destroy()

	var acquireErr, tryErr error
	require.NotPanics(t, func() {
		acquireErr = l.Acquire()
		tryErr = l.TryAcquire(context.Background())
	})
	require.True(t, errors.Is(acquireErr, ErrLockerDestroyed))
	require.True(t, errors.Is(tryErr, ErrLockerDestroyed))
	require.False(t, l.IsLocked())
}

func TestLockerDestroyIsIdempotent(t *testing.T) {
	l := NewLocker()
	require.NoError(t, l.Acquire())
	l.Destroy()
	require.NotPanics(t, l.Destroy)

	// The locker is still usable for read-only calls afterwards.
	require.False(t, l.IsLocked())
	l.Release()
}

// A caller waiting for the lock gives up when the locker is destroyed.
func TestLockerWaitersAreReleasedOnDestroy(t *testing.T) {
	l := NewLocker()
	require.NoError(t, l.Acquire())

	errs := make(chan error, 1)
	go func() {
		errs <- l.TryAcquire(context.Background())
	}()

	time.Sleep(50 * time.Millisecond)
	l.Destroy()

	select {
	case err := <-errs:
		require.True(t, errors.Is(err, ErrLockerDestroyed), "got %v", err)
	case <-time.After(time.Second):
		t.Fatal("expected the waiting caller to return once the locker was destroyed")
	}
	require.False(t, l.IsLocked())
}
