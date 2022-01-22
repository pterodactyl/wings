package events

import (
	"testing"
	"time"

	. "github.com/franela/goblin"
)

func TestNewBus(t *testing.T) {
	g := Goblin(t)
	bus := NewBus()

	g.Describe("NewBus", func() {
		g.It("is not nil", func() {
			g.Assert(bus).IsNotNil("Bus expected to not be nil")
			g.Assert(bus.listeners).IsNotNil("Bus#listeners expected to not be nil")
		})
	})
}

func TestBus_Off(t *testing.T) {
	g := Goblin(t)

	const topic = "test"

	g.Describe("Off", func() {
		g.It("unregisters listener", func() {
			bus := NewBus()

			g.Assert(bus.listeners[topic]).IsNotNil()
			g.Assert(len(bus.listeners[topic])).IsZero()
			listener := make(chan Event)
			bus.On(listener, topic)
			g.Assert(len(bus.listeners[topic])).Equal(1, "Listener was not registered")

			bus.Off(listener, topic)
			g.Assert(len(bus.listeners[topic])).Equal(0, "Topic still has one or more listeners")
		})

		g.It("unregisters correct listener", func() {
			bus := NewBus()

			listener := make(chan Event)
			listener2 := make(chan Event)
			listener3 := make(chan Event)
			bus.On(listener, topic)
			bus.On(listener2, topic)
			bus.On(listener3, topic)
			g.Assert(len(bus.listeners[topic])).Equal(3, "Listeners were not registered")

			bus.Off(listener, topic)
			bus.Off(listener3, topic)
			g.Assert(len(bus.listeners[topic])).Equal(1, "Expected 1 listener to remain")

			if bus.listeners[topic][0] != listener2 {
				// A normal Assert does not properly compare channels.
				g.Fail("wrong listener unregistered")
			}

			// Cleanup
			bus.Off(listener2, topic)
		})
	})
}

func TestBus_On(t *testing.T) {
	g := Goblin(t)

	const topic = "test"

	g.Describe("On", func() {
		g.It("registers listener", func() {
			bus := NewBus()

			g.Assert(bus.listeners[topic]).IsNotNil()
			g.Assert(len(bus.listeners[topic])).IsZero()
			listener := make(chan Event)
			bus.On(listener, topic)
			g.Assert(len(bus.listeners[topic])).Equal(1, "Listener was not registered")

			if bus.listeners[topic][0] != listener {
				// A normal Assert does not properly compare channels.
				g.Fail("wrong listener registered")
			}

			// Cleanup
			bus.Off(listener, topic)
		})
	})
}

func TestBus_Publish(t *testing.T) {
	g := Goblin(t)

	const topic = "test"
	const message = "this is a test message!"

	g.Describe("Publish", func() {
		g.It("publishes message", func() {
			bus := NewBus()

			g.Assert(bus.listeners[topic]).IsNotNil()
			g.Assert(len(bus.listeners[topic])).IsZero()
			listener := make(chan Event)
			bus.On(listener, topic)
			g.Assert(len(bus.listeners[topic])).Equal(1, "Listener was not registered")

			done := make(chan struct{}, 1)
			go func() {
				select {
				case m := <-listener:
					g.Assert(m.Topic).Equal(topic)
					g.Assert(m.Data).Equal(message)
				case <-time.After(1 * time.Second):
					g.Fail("listener did not receive message in time")
				}
				done <- struct{}{}
			}()
			bus.Publish(topic, message)
			<-done

			// Cleanup
			bus.Off(listener, topic)
		})

		g.It("publishes message to all listeners", func() {
			bus := NewBus()

			g.Assert(bus.listeners[topic]).IsNotNil()
			g.Assert(len(bus.listeners[topic])).IsZero()
			listener := make(chan Event)
			listener2 := make(chan Event)
			listener3 := make(chan Event)
			bus.On(listener, topic)
			bus.On(listener2, topic)
			bus.On(listener3, topic)
			g.Assert(len(bus.listeners[topic])).Equal(3, "Listener was not registered")

			done := make(chan struct{}, 1)
			go func() {
				for i := 0; i < 3; i++ {
					select {
					case m := <-listener:
						g.Assert(m.Topic).Equal(topic)
						g.Assert(m.Data).Equal(message)
					case m := <-listener2:
						g.Assert(m.Topic).Equal(topic)
						g.Assert(m.Data).Equal(message)
					case m := <-listener3:
						g.Assert(m.Topic).Equal(topic)
						g.Assert(m.Data).Equal(message)
					case <-time.After(1 * time.Second):
						g.Fail("all listeners did not receive the message in time")
						i = 3
					}
				}

				done <- struct{}{}
			}()
			bus.Publish(topic, message)
			<-done

			// Cleanup
			bus.Off(listener, topic)
			bus.Off(listener2, topic)
			bus.Off(listener3, topic)
		})
	})
}

func TestBus_Destroy(t *testing.T) {
	g := Goblin(t)

	g.Describe("Destroy", func() {
		g.It("unsubscribes and closes all listeners", func() {
			bus := NewBus()

			listener := make(chan Event)
			bus.On(listener, "test")

			done := make(chan struct{}, 1)
			go func() {
				select {
				case m := <-listener:
					g.Assert(m).IsZero()
				case <-time.After(1 * time.Second):
					g.Fail("listener did not receive message in time")
				}
				done <- struct{}{}
			}()
			bus.Destroy()
			<-done

			g.Assert(bus.listeners).Equal(map[string][]Listener{})
		})

		// This is a check that ensures Destroy only closes each listener
		// channel once, even if it is subscribed to multiple topics.
		//
		// Closing a channel multiple times will cause a runtime panic, which
		// I'm pretty sure we don't want.
		g.It("unsubscribes and closes channel only once", func() {
			bus := NewBus()

			listener := make(chan Event)
			bus.On(listener, "test", "test2", "test3", "test4", "test5")

			done := make(chan struct{}, 1)
			go func() {
				select {
				case m := <-listener:
					g.Assert(m).IsZero()
				case <-time.After(1 * time.Second):
					g.Fail("listener did not receive message in time")
				}
				done <- struct{}{}
			}()
			bus.Destroy()
			<-done

			g.Assert(bus.listeners).Equal(map[string][]Listener{})
		})
	})
}
