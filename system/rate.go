package system

import (
	"sync"
	"time"
)

// Rate defines a rate limiter of n items (limit) per duration of time.
type Rate struct {
	mu       sync.Mutex
	limit    uint64
	duration time.Duration
	count    uint64
	last     time.Time
}

func NewRate(limit uint64, duration time.Duration) *Rate {
	return &Rate{
		limit:    limit,
		duration: duration,
		last:     time.Now(),
	}
}

// Try returns true if under the rate limit defined, or false if the rate limit
// has been exceeded for the current duration.
func (r *Rate) Try() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now()
	// If it has been more than the duration, reset the timer and count.
	if now.Sub(r.last) > r.duration {
		r.count = 0
		r.last = now
	}
	if (r.count + 1) > r.limit {
		return false
	}
	// Hit this once, and return.
	r.count = r.count + 1
	return true
}

// Reset resets the internal state of the rate limiter back to zero.
func (r *Rate) Reset() {
	r.mu.Lock()
	r.count = 0
	r.last = time.Now()
	r.mu.Unlock()
}
