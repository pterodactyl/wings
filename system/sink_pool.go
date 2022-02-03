package system

import (
	"sync"
	"time"
)

// SinkName represents one of the registered sinks for a server.
type SinkName string

const (
	// LogSink handles console output for game servers, including messages being
	// sent via Wings to the console instance.
	LogSink SinkName = "log"
	// InstallSink handles installation output for a server.
	InstallSink SinkName = "install"
)

// SinkPool represents a pool with sinks.
type SinkPool struct {
	mu    sync.RWMutex
	sinks []chan []byte
}

// NewSinkPool returns a new empty SinkPool. A sink pool generally lives with a
// server instance for it's full lifetime.
func NewSinkPool() *SinkPool {
	return &SinkPool{}
}

// On adds a channel to the sink pool instance.
func (p *SinkPool) On(c chan []byte) {
	p.mu.Lock()
	p.sinks = append(p.sinks, c)
	p.mu.Unlock()
}

// Off removes a given channel from the sink pool. If no matching sink is found
// this function is a no-op. If a matching channel is found, it will be removed.
func (p *SinkPool) Off(c chan []byte) {
	p.mu.Lock()
	defer p.mu.Unlock()

	sinks := p.sinks
	for i, sink := range sinks {
		if c != sink {
			continue
		}

		// We need to maintain the order of the sinks in the slice we're tracking,
		// so shift everything to the left, rather than changing the order of the
		// elements.
		copy(sinks[i:], sinks[i+1:])
		sinks[len(sinks)-1] = nil
		sinks = sinks[:len(sinks)-1]
		p.sinks = sinks

		// Avoid a panic if the sink channel is nil at this point.
		if c != nil {
			close(c)
		}

		return
	}
}

// Destroy destroys the pool by removing and closing all sinks and destroying
// all of the channels that are present.
func (p *SinkPool) Destroy() {
	p.mu.Lock()
	defer p.mu.Unlock()

	for _, c := range p.sinks {
		if c != nil {
			close(c)
		}
	}

	p.sinks = nil
}

// Push sends a given message to each of the channels registered in the pool.
// This will use a Ring Buffer channel in order to avoid blocking the channel
// sends, and attempt to push though the most recent messages in the queue in
// favor of the oldest messages.
//
// If the channel becomes full and isn't being drained fast enough, this
// function will remove the oldest message in the channel, and then push the
// message that it got onto the end, effectively making the channel a rolling
// buffer.
//
// There is a potential for data to be lost when passing it through this
// function, but only in instances where the channel buffer is full and the
// channel is not drained fast enough, in which case dropping messages is most
// likely the best option anyways. This uses waitgroups to allow every channel
// to attempt its send concurrently thus making the total blocking time of this
// function "O(1)" instead of "O(n)".
func (p *SinkPool) Push(data []byte) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	var wg sync.WaitGroup
	wg.Add(len(p.sinks))
	for _, c := range p.sinks {
		go func(c chan []byte) {
			defer wg.Done()
			select {
			case c <- data:
			case <-time.After(time.Millisecond * 10):
				// If there is nothing in the channel to read, but we also cannot write
				// to the channel, just skip over sending data. If we don't do this you'll
				// end up blocking the application on the channel read below.
				if len(c) == 0 {
					break
				}
				<-c
				c <- data
			}
		}(c)
	}
	wg.Wait()
}
