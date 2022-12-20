package events

import (
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
	enc, err := json.Marshal(Event{Topic: topic, Data: data})
	if err != nil {
		panic(errors.WithStack(err))
	}
	b.Push(enc)
}

// MustDecode decodes the event byte slice back into an events.Event struct or
// panics if an error is encountered during this process.
func MustDecode(data []byte) (e Event) {
	if err := DecodeTo(data, &e); err != nil {
		panic(err)
	}
	return
}

// DecodeTo decodes a byte slice of event data into the given interface.
func DecodeTo(data []byte, v interface{}) error {
	if err := json.Unmarshal(data, &v); err != nil {
		return errors.Wrap(err, "events: failed to decode byte slice")
	}
	return nil
}
