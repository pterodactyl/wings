package system

import (
	"context"
	"sync"

	"emperror.dev/errors"
)

var ErrLockerLocked = errors.Sentinel("locker: cannot acquire lock, already locked")

type Locker struct {
	mu sync.RWMutex
	ch chan bool
}

// NewLocker returns a new Locker instance.
func NewLocker() *Locker {
	return &Locker{
		ch: make(chan bool, 1),
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
	case l.ch <- true:
		return nil
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

// Destroy cleans up the power locker by closing the channel.
func (l *Locker) Destroy() {
	l.mu.Lock()
	if l.ch != nil {
		select {
		case <-l.ch:
		default:
		}
		close(l.ch)
	}
	l.mu.Unlock()
}
