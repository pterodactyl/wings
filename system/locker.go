package system

import (
	"context"
	"sync"

	"emperror.dev/errors"
)

var (
	ErrLockerLocked    = errors.Sentinel("locker: cannot acquire lock, already locked")
	ErrLockerDestroyed = errors.Sentinel("locker: cannot acquire lock, locker destroyed")
)

type Locker struct {
	mu sync.RWMutex
	ch chan bool
	// done is closed when the locker is destroyed.
	done chan struct{}
}

// NewLocker returns a new Locker instance.
func NewLocker() *Locker {
	return &Locker{
		ch:   make(chan bool, 1),
		done: make(chan struct{}),
	}
}

// IsLocked returns the current state of the locker channel. If there is
// currently a value in the channel, it is assumed to be locked.
func (l *Locker) IsLocked() bool {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return len(l.ch) == 1
}

// Acquire will acquire the power lock if it is not currently locked. If it is
// already locked, acquire will fail to acquire the lock, and will return false.
func (l *Locker) Acquire() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.destroyed() {
		return ErrLockerDestroyed
	}
	select {
	case l.ch <- true:
	default:
		return ErrLockerLocked
	}
	return nil
}

// TryAcquire will attempt to acquire a power-lock until the context provided
// is canceled.
func (l *Locker) TryAcquire(ctx context.Context) error {
	select {
	case <-l.done:
		return ErrLockerDestroyed
	default:
	}
	select {
	case l.ch <- true:
		// The locker may have been destroyed while waiting.
		l.mu.Lock()
		defer l.mu.Unlock()
		if l.destroyed() {
			select {
			case <-l.ch:
			default:
			}
			return ErrLockerDestroyed
		}
		return nil
	case <-l.done:
		return ErrLockerDestroyed
	case <-ctx.Done():
		if err := ctx.Err(); err != nil {
			if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
				return ErrLockerLocked
			}
		}
		return nil
	}
}

// Release will drain the locker channel so that we can properly re-acquire it
// at a later time. If the channel is not currently locked this function is a
// no-op and will immediately return.
func (l *Locker) Release() {
	l.mu.Lock()
	select {
	case <-l.ch:
	default:
	}
	l.mu.Unlock()
}

// Destroy releases the lock and stops it from being acquired again. Calling it
// more than once is a no-op.
func (l *Locker) Destroy() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.destroyed() {
		return
	}
	select {
	case <-l.ch:
	default:
	}
	close(l.done)
}

// destroyed reports whether Destroy has been called. The caller must hold mu.
func (l *Locker) destroyed() bool {
	select {
	case <-l.done:
		return true
	default:
		return false
	}
}
