// Package events2 ...
package events

import (
	"sync"
)

type Listener chan Event

// Event .
type Event struct {
	// Topic .
	Topic string
	// Data .
	Data interface{}
}

// Bus .
type Bus struct {
	listenersMx sync.Mutex
	listeners   map[string][]Listener
}

// NewBus .
func NewBus() *Bus {
	return &Bus{
		listeners: make(map[string][]Listener),
	}
}

func (b *Bus) Off(listener Listener, topics ...string) {
	b.listenersMx.Lock()
	defer b.listenersMx.Unlock()

	for _, topic := range topics {
		b.off(topic, listener)
	}
}

func (b *Bus) off(topic string, listener Listener) bool {
	listeners, ok := b.listeners[topic]
	if !ok {
		return false
	}
	for i, l := range listeners {
		if l != listener {
			continue
		}

		listeners = append(listeners[:i], listeners[i+1:]...)
		b.listeners[topic] = listeners
		return true
	}
	return false
}

func (b *Bus) On(listener Listener, topics ...string) {
	b.listenersMx.Lock()
	defer b.listenersMx.Unlock()

	for _, topic := range topics {
		b.on(topic, listener)
	}
}

func (b *Bus) on(topic string, listener Listener) {
	listeners, ok := b.listeners[topic]
	if !ok {
		b.listeners[topic] = []Listener{listener}
	} else {
		b.listeners[topic] = append(listeners, listener)
	}
}

func (b *Bus) Publish(topic string, data interface{}) {
	b.listenersMx.Lock()
	defer b.listenersMx.Unlock()

	listeners, ok := b.listeners[topic]
	if !ok {
		return
	}
	if len(listeners) < 1 {
		return
	}

	var wg sync.WaitGroup
	event := Event{Topic: topic, Data: data}
	for _, listener := range listeners {
		l := listener
		wg.Add(1)
		go func(l Listener, event Event) {
			defer wg.Done()
			l <- event
		}(l, event)
	}
	wg.Wait()
}

func (b *Bus) Destroy() {
	b.listenersMx.Lock()
	defer b.listenersMx.Unlock()

	for _, listeners := range b.listeners {
		for _, listener := range listeners {
			close(listener)
		}
	}

	b.listeners = make(map[string][]Listener)
}
