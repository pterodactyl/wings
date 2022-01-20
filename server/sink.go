package server

import (
	"sync"
	"time"
)

// sinkPool represents a pool with sinks.
type sinkPool struct {
	mx    sync.RWMutex
	sinks []chan []byte
}

// newSinkPool returns a new empty sinkPool.
func newSinkPool() *sinkPool {
	return &sinkPool{}
}

// Off removes a sink from the pool.
func (p *sinkPool) Off(c chan []byte) {
	p.mx.Lock()
	defer p.mx.Unlock()

	sinks := p.sinks

	for i, sink := range sinks {
		if c != sink {
			continue
		}
		copy(sinks[i:], sinks[i+1:])
		sinks[len(sinks)-1] = nil
		sinks = sinks[:len(sinks)-1]
		p.sinks = sinks
		close(c)
		return
	}
}

// On adds a sink on the pool.
func (p *sinkPool) On(c chan []byte) {
	p.mx.Lock()
	defer p.mx.Unlock()

	p.sinks = append(p.sinks, c)
}

// Destroy destroys the pool by removing and closing all sinks.
func (p *sinkPool) Destroy() {
	p.mx.Lock()
	defer p.mx.Unlock()

	for _, c := range p.sinks {
		close(c)
	}

	p.sinks = nil
}

// Push pushes a message to all registered sinks.
func (p *sinkPool) Push(v []byte) {
	p.mx.RLock()
	for _, c := range p.sinks {
		// TODO: should this be done in parallel?
		select {
		// Send the log output to the channel
		case c <- v:
		// Timeout after 100 milliseconds, this will cause the write to the channel to be cancelled.
		case <-time.After(100 * time.Millisecond):
		}
	}
	p.mx.RUnlock()
}
