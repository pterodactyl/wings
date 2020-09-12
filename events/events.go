package events

import (
	"encoding/json"
	"github.com/pkg/errors"
	"reflect"
	"strings"
	"sync"
)

type Event struct {
	Data  string
	Topic string
}

type EventBus struct {
	mu        sync.RWMutex
	callbacks map[string][]*func(Event)
}

func New() *EventBus {
	return &EventBus{
		callbacks: make(map[string][]*func(Event)),
	}
}

// Publish data to a given topic.
func (e *EventBus) Publish(topic string, data string) {
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

	e.mu.RLock()
	defer e.mu.RUnlock()

	// Acquire a read lock and loop over all of the channels registered for the topic. This
	// avoids a panic crash if the process tries to unregister the channel while this routine
	// is running.
	if _, ok := e.callbacks[t]; ok {
		evt := Event{Data: data, Topic: topic}
		for _, callback := range e.callbacks[t] {
			go func(evt Event, callback func(Event)) {
				callback(evt)
			}(evt, *callback)
		}
	}
}

// Publishes a JSON message to a given topic.
func (e *EventBus) PublishJson(topic string, data interface{}) error {
	b, err := json.Marshal(data)
	if err != nil {
		return errors.WithStack(err)
	}

	e.Publish(topic, string(b))

	return nil
}

// Register a callback function that will be executed each time one of the events using the topic
// name is called.
func (e *EventBus) On(topic string, callback *func(Event)) {
	e.mu.Lock()
	defer e.mu.Unlock()

	// Check if this topic has been registered at least once for the event listener, and if
	// not create an empty struct for the topic.
	if _, exists := e.callbacks[topic]; !exists {
		e.callbacks[topic] = make([]*func(Event), 0)
	}

	// If this callback is not already registered as an event listener, go ahead and append
	// it to the array of callbacks for this topic.
	if e.index(topic, reflect.ValueOf(callback)) < 0 {
		e.callbacks[topic] = append(e.callbacks[topic], callback)
	}
}

// Removes an event listener from the bus.
func (e *EventBus) Off(topic string, callback *func(Event)) {
	e.mu.Lock()
	defer e.mu.Unlock()

	i := e.index(topic, reflect.ValueOf(callback))

	// If i < 0 it means there was no index found for the given callback, meaning it was
	// never registered or was already unregistered from the listeners. Also double check
	// that we didn't somehow escape the length of the topic callback (not sure how that
	// would happen, but lets avoid a panic condition).
	if i < 0 || i >= len(e.callbacks[topic]) {
		return
	}

	// We can assume that the topic still exists at this point since we acquire an exclusive
	// lock on the process, and the "e.index" function cannot return a value >= 0 if there is
	// no topic already existing.
	e.callbacks[topic] = append(e.callbacks[topic][:i], e.callbacks[topic][i+1:]...)
}

// Removes all of the event listeners that have been registered for any topic.
func (e *EventBus) RemoveAll() {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.callbacks = make(map[string][]*func(Event))
}

// Finds the index of a given callback in the topic by comparing all of the registered callback
// pointers to the passed function. This function does not aquire a lock as it should only be called
// within the confines of a function that has already acquired a lock for the duration of the lookup.
func (e *EventBus) index(topic string, v reflect.Value) int {
	if _, ok := e.callbacks[topic]; ok {
		for i, handler := range e.callbacks[topic] {
			if reflect.ValueOf(handler).Pointer() == v.Pointer() {
				return i
			}
		}
	}

	return -1
}
