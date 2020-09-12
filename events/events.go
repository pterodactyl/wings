package events

import (
	"encoding/json"
	"fmt"
	"github.com/sasha-s/go-deadlock"
	"strings"
)

type Event struct {
	Data  string
	Topic string
}

type EventBus struct {
	deadlock.RWMutex

	subscribers map[string]map[chan Event]struct{}
}

func New() *EventBus {
	return &EventBus{
		subscribers: make(map[string]map[chan Event]struct{}),
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

	// Acquire a read lock and loop over all of the channels registered for the topic. This
	// avoids a panic crash if the process tries to unregister the channel while this routine
	// is running.
	go func() {
		fmt.Println("publish acquiring RLOCK")
		e.RLock()
		fmt.Println("publish got RLOCK")
		defer e.RUnlock()
		defer fmt.Println("publish released RLOCK")

		if ch, ok := e.subscribers[t]; ok {
			for channel := range ch {
				channel <- Event{Data: data, Topic: topic}
			}
		}
	}()
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
	fmt.Println("locking for subscribe")
	e.Lock()
	fmt.Println("acquired lock for subscribe")
	defer e.Unlock()
	defer fmt.Println("released lock for subscribe")

	if _, exists := e.subscribers[topic]; !exists {
		e.subscribers[topic] = make(map[chan Event]struct{})
	}

	// Only set the channel if there is not currently a matching one for this topic. This
	// avoids registering two identical listeners for the same topic and causing pain in
	// the unsubscribe functionality as well.
	if _, exists := e.subscribers[topic][ch]; !exists {
		e.subscribers[topic][ch] = struct{}{}
	}
}

// Unsubscribe a channel from a given topic.
func (e *EventBus) Unsubscribe(topic string, ch chan Event) {
	fmt.Println("checking for lock in unsubscribe")
	e.Lock()
	fmt.Println("acquired lock for unsubscribe")
	defer e.Unlock()
	defer fmt.Println("released lock for unsubscribe")

	if _, exists := e.subscribers[topic][ch]; exists {
		delete(e.subscribers[topic], ch)
	}
}

// Removes all of the event listeners for the server. This is used when a server
// is being deleted to avoid a bunch of de-reference errors cropping up. Obviously
// should also check elsewhere and handle a server reference going nil, but this
// won't hurt.
func (e *EventBus) UnsubscribeAll() {
	fmt.Println("unsubscribe all pre-lock")
	e.Lock()
	fmt.Println("unsubscribe all locked")
	defer e.Unlock()
	defer fmt.Println("unsubscribe all lock-release")

	// Reset the entire struct into an empty map.
	e.subscribers = make(map[string]map[chan Event]struct{})
}
