package server

import (
	"sync"
	"time"
)

// sinkPool .
type sinkPool struct {
	mx    sync.RWMutex
	sinks []chan []byte
}

// newSinkPool .
func newSinkPool() *sinkPool {
	return &sinkPool{}
}

// On .
func (p *sinkPool) On(c chan []byte) {
	p.mx.Lock()
	defer p.mx.Unlock()

	p.sinks = append(p.sinks, c)
}

// Off .
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
		return
	}
}

// Destroy .
func (p *sinkPool) Destroy() {
	p.mx.Lock()
	defer p.mx.Unlock()

	for _, c := range p.sinks {
		close(c)
	}

	p.sinks = nil
}

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
