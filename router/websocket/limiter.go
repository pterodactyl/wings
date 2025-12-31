package websocket

import (
	"sync"
	"time"

	"golang.org/x/time/rate"
)

type LimiterBucket struct {
	mu        sync.RWMutex
	limits    map[Event]*rate.Limiter
	throttles map[Event]bool
}

func (h *Handler) IsThrottled(e Event) bool {
	l := h.limiter.For(e)

	h.limiter.mu.Lock()
	defer h.limiter.mu.Unlock()

	if l.Allow() {
		h.limiter.throttles[e] = false

		return false
	}

	// If not allowed, track the throttling and send an event over the wire
	// if one wasn't already sent in the same throttling period.
	if v, ok := h.limiter.throttles[e]; !v || !ok {
		h.limiter.throttles[e] = true
		h.Logger().WithField("event", e).Debug("throttling websocket due to event volume")

		_ = h.unsafeSendJson(&Message{Event: ThrottledEvent, Args: []string{string(e)}})
	}

	return true
}

func NewLimiter() *LimiterBucket {
	return &LimiterBucket{
		limits:    make(map[Event]*rate.Limiter, 4),
		throttles: make(map[Event]bool, 4),
	}
}

// For returns the internal rate limiter for the given event type. In most
// cases this is a shared rate limiter for events, but certain "heavy" or low-frequency
// events implement their own limiters.
func (l *LimiterBucket) For(e Event) *rate.Limiter {
	name := limiterName(e)

	l.mu.RLock()
	if v, ok := l.limits[name]; ok {
		l.mu.RUnlock()
		return v
	}

	l.mu.RUnlock()
	l.mu.Lock()
	defer l.mu.Unlock()

	limit, burst := limitValuesFor(e)
	l.limits[name] = rate.NewLimiter(limit, burst)

	return l.limits[name]
}

// limitValuesFor returns the underlying limit and burst value for the given event.
func limitValuesFor(e Event) (rate.Limit, int) {
	// Twice every five seconds.
	if e == AuthenticationEvent || e == SendServerLogsEvent {
		return rate.Every(time.Second * 5), 2
	}

	// 10 per second.
	if e == SendCommandEvent {
		return rate.Every(time.Second), 10
	}

	// 4 per second.
	return rate.Every(time.Second), 4
}

func limiterName(e Event) Event {
	if e == AuthenticationEvent || e == SendServerLogsEvent || e == SendCommandEvent {
		return e
	}

	return "_default"
}
