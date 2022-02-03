package events

import (
	"strings"

	"emperror.dev/errors"
	"github.com/goccy/go-json"
	"github.com/pterodactyl/wings/system"
)

// Event represents an Event sent over a Bus.
type Event struct {
	Topic string
	Data  interface{}
}

// Bus represents an Event Bus.
type Bus struct {
	*system.SinkPool
}

// NewBus returns a new empty Bus. This is simply a nicer wrapper around the
// system.SinkPool implementation that allows for more simplistic usage within
// the codebase.
//
// All of the events emitted out of this bus are byte slices that can be decoded
// back into an events.Event interface.
func NewBus() *Bus {
	return &Bus{
		system.NewSinkPool(),
	}
}

// Publish publishes a message to the Bus.
func (b *Bus) Publish(topic string, data interface{}) {
	// Some of our actions for the socket support passing a more specific namespace,
	// such as "backup completed:1234" to indicate which specific backup was completed.
	//
	// In these cases, we still need to send the event using the standard listener
	// name of "backup completed".
	if strings.Contains(topic, ":") {
		parts := strings.SplitN(topic, ":", 2)
		if len(parts) == 2 {
			topic = parts[0]
		}
	}

	enc, err := json.Marshal(Event{Topic: topic, Data: data})
	if err != nil {
		panic(errors.WithStack(err))
	}
	b.Push(enc)
}

// MustDecode decodes the event byte slice back into an events.Event struct or
// panics if an error is encountered during this process.
func MustDecode(data []byte) (e Event) {
	MustDecodeTo(data, &e)
	return
}

func MustDecodeTo(data []byte, v interface{}) {
	if err := json.Unmarshal(data, &v); err != nil {
		panic(errors.Wrap(err, "events: failed to decode event data into interface"))
	}
}
