package server

import (
	"encoding/json"
	"strings"
	"sync"
)

// Defines all of the possible output events for a server.
// noinspection GoNameStartsWithPackageName
const (
	DaemonMessageEvent    = "daemon message"
	InstallOutputEvent    = "install output"
	InstallStartedEvent   = "install started"
	InstallCompletedEvent = "install completed"
	ConsoleOutputEvent    = "console output"
	StatusEvent           = "status"
	StatsEvent            = "stats"
	BackupCompletedEvent  = "backup completed"
)

type Event struct {
	Data  string
	Topic string
}

type EventBus struct {
	sync.RWMutex

	subscribers map[string][]chan Event
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
	e.RLock()
	defer e.RUnlock()

	t := topic
	// Some of our topics for the socket support passing a more specific namespace,
	// such as "backup completed:1234" to indicate which specific backup was completed.
	//
	// In these cases, we still need to the send the event using the standard listener
	// name of "backup completed".
	if strings.Contains(topic, ":") {
		parts := strings.SplitN(topic, ":", 2)

		if len(parts) == 2 {
			t = parts[0]
		}
	}

	if ch, ok := e.subscribers[t]; ok {
		go func(data Event, cs []chan Event) {
			for _, channel := range cs {
				channel <- data
			}
		}(Event{Data: data, Topic: topic}, ch)
	}
}

func (e *EventBus) PublishJson(topic string, data interface{}) error {
	b, err := json.Marshal(data)
	if err != nil {
		return err
	}

	e.Publish(topic, string(b))

	return nil
}

// Subscribe to an emitter topic using a channel.
func (e *EventBus) Subscribe(topic string, ch chan Event) {
	e.Lock()
	defer e.Unlock()

	if p, ok := e.subscribers[topic]; ok {
		e.subscribers[topic] = append(p, ch)
	} else {
		e.subscribers[topic] = append([]chan Event{}, ch)
	}
}

// Unsubscribe a channel from a topic.
func (e *EventBus) Unsubscribe(topic string, ch chan Event) {
	e.Lock()
	defer e.Unlock()

	if _, ok := e.subscribers[topic]; ok {
		for i := range e.subscribers[topic] {
			if ch == e.subscribers[topic][i] {
				e.subscribers[topic] = append(e.subscribers[topic][:i], e.subscribers[topic][i+1:]...)
			}
		}
	}
}

// Removes all of the event listeners for the server. This is used when a server
// is being deleted to avoid a bunch of de-reference errors cropping up. Obviously
// should also check elsewhere and handle a server reference going nil, but this
// won't hurt.
func (e *EventBus) UnsubscribeAll() {
	e.Lock()
	defer e.Unlock()

	// Loop over all of the subscribers and just remove all of the events
	// for them.
	for t := range e.subscribers {
		e.subscribers[t] = make([]chan Event, 0)
	}
}
