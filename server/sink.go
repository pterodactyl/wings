package server

import (
	"sync"
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

// sinkPool represents a pool with sinks.
type sinkPool struct {
	mu    sync.RWMutex
	sinks []chan []byte
}

// newSinkPool returns a new empty sinkPool. A sink pool generally lives with a
// server instance for it's full lifetime.
func newSinkPool() *sinkPool {
	return &sinkPool{}
}

// On adds a channel to the sink pool instance.
func (p *sinkPool) On(c chan []byte) {
	p.mu.Lock()
	p.sinks = append(p.sinks, c)
	p.mu.Unlock()
}

// Off removes a given channel from the sink pool. If no matching sink is found
// this function is a no-op. If a matching channel is found, it will be removed.
func (p *sinkPool) Off(c chan []byte) {
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

		// Update our tracked sinks, and close the matched channel.
		p.sinks = sinks
		close(c)

		return
	}
}

// Destroy destroys the pool by removing and closing all sinks and destroying
// all of the channels that are present.
func (p *sinkPool) Destroy() {
	p.mu.Lock()
	defer p.mu.Unlock()

	for _, c := range p.sinks {
		close(c)
	}

	p.sinks = nil
}

// Push sends a given message to each of the channels registered in the pool.
func (p *sinkPool) Push(data []byte) {
	p.mu.RLock()
	for _, c := range p.sinks {
		select {
		// Send the event data over to the channels.
		case c <- data:
		}
	}
	p.mu.RUnlock()
}

// Sink returns the instantiated and named sink for a server. If the sink has
// not been configured yet this function will cause a panic condition.
func (s *Server) Sink(name SinkName) *sinkPool {
	sink, ok := s.sinks[name]
	if !ok {
		s.Log().Fatalf("attempt to access nil sink: %s", name)
	}
	return sink
}

// DestroyAllSinks iterates over all of the sinks configured for the server and
// destroys their instances. Note that this will cause a panic if you attempt
// to call Server.Sink() again after. This function is only used when a server
// is being deleted from the system.
func (s *Server) DestroyAllSinks() {
	s.Log().Info("destroying all registered sinks for server instance")
	for _, sink := range s.sinks {
		sink.Destroy()
	}
}
