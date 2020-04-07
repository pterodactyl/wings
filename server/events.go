package server

import (
	"sync"
)

// Defines all of the possible output events for a server.
// noinspection GoNameStartsWithPackageName
const (
	DaemonMessageEvent   = "daemon message"
	InstallOutputEvent   = "install output"
	ConsoleOutputEvent   = "console output"
	StatusEvent          = "status"
	StatsEvent           = "stats"
	BackupCompletedEvent = "backup completed"
)

type Event struct {
	Data  string
	Topic string
}

type EventBus struct {
	subscribers map[string][]chan Event
	mu          sync.Mutex
}

// Returns the server's emitter instance.
func (s *Server) Events() *EventBus {
	if s.emitter == nil {
		s.emitter = &EventBus{
			subscribers: map[string][]chan Event{},
		}
	}

	return s.emitter
}

// Publish data to a given topic.
func (e *EventBus) Publish(topic string, data string) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if ch, ok := e.subscribers[topic]; ok {
		go func(data Event, cs []chan Event) {
			for _, channel := range cs {
				channel <- data
			}
		}(Event{Data: data, Topic: topic}, ch)
	}
}

// Subscribe to an emitter topic using a channel.
func (e *EventBus) Subscribe(topic string, ch chan Event) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if p, ok := e.subscribers[topic]; ok {
		e.subscribers[topic] = append(p, ch)
	} else {
		e.subscribers[topic] = append([]chan Event{}, ch)
	}
}

// Unsubscribe a channel from a topic.
func (e *EventBus) Unsubscribe(topic string, ch chan Event) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if _, ok := e.subscribers[topic]; ok {
		for i := range e.subscribers[topic] {
			if ch == e.subscribers[topic][i] {
				e.subscribers[topic] = append(e.subscribers[topic][:i], e.subscribers[topic][i+1:]...)
			}
		}
	}
}