package server

import (
	"reflect"
	"sync"
	"testing"

	. "github.com/franela/goblin"
)

func MutexLocked(m *sync.RWMutex) bool {
	v := reflect.ValueOf(m).Elem()

	state := v.FieldByName("w").FieldByName("state")

	return state.Int()&1 == 1 || v.FieldByName("readerCount").Int() > 0
}

func Test(t *testing.T) {
	g := Goblin(t)

	g.Describe("SinkPool#On", func() {
		g.It("pushes additional channels to a sink", func() {
			pool := &sinkPool{}

			g.Assert(pool.sinks).IsZero()

			c1 := make(chan []byte, 1)
			pool.On(c1)

			g.Assert(len(pool.sinks)).Equal(1)
			g.Assert(MutexLocked(&pool.mu)).IsFalse()
		})
	})

	g.Describe("SinkPool#Off", func() {
		var pool *sinkPool
		g.BeforeEach(func() {
			pool = &sinkPool{}
		})

		g.It("works when no sinks are registered", func() {
			ch := make(chan []byte, 1)

			g.Assert(pool.sinks).IsZero()
			pool.Off(ch)

			g.Assert(pool.sinks).IsZero()
			g.Assert(MutexLocked(&pool.mu)).IsFalse()
		})

		g.It("does not remove any sinks when the channel does not match", func() {
			ch := make(chan []byte, 1)
			ch2 := make(chan []byte, 1)

			pool.On(ch)
			g.Assert(len(pool.sinks)).Equal(1)

			pool.Off(ch2)
			g.Assert(len(pool.sinks)).Equal(1)
			g.Assert(pool.sinks[0]).Equal(ch)
			g.Assert(MutexLocked(&pool.mu)).IsFalse()
		})

		g.It("removes a channel and maintains the order", func() {
			channels := make([]chan []byte, 8)
            for i := 0; i < len(channels); i++ {
				channels[i] = make(chan []byte, 1)
                pool.On(channels[i])
            }

			g.Assert(len(pool.sinks)).Equal(8)

			pool.Off(channels[2])
			g.Assert(len(pool.sinks)).Equal(7)
			g.Assert(pool.sinks[1]).Equal(channels[1])
			g.Assert(pool.sinks[2]).Equal(channels[3])
			g.Assert(MutexLocked(&pool.mu)).IsFalse()
		})

		g.It("does not panic if a nil channel is provided", func() {
			ch := make([]chan []byte, 1)

			defer func () {
				if r := recover(); r != nil {
                    g.Fail("removing a nil channel should not cause a panic")
                }
			}()

			pool.On(ch[0])
			pool.Off(ch[0])

			g.Assert(len(pool.sinks)).Equal(0)
		})
	})

	g.Describe("SinkPool#Push", func() {
		var pool *sinkPool
		g.BeforeEach(func() {
			pool = &sinkPool{}
		})

		g.It("works when no sinks are registered", func() {
			g.Assert(len(pool.sinks)).IsZero()
			pool.Push([]byte("test"))
			g.Assert(MutexLocked(&pool.mu)).IsFalse()
		})

		g.It("sends data to every registered sink", func() {
			ch1 := make(chan []byte, 1)
			ch2 := make(chan []byte, 1)

			pool.On(ch1)
			pool.On(ch2)

			g.Assert(len(pool.sinks)).Equal(2)
			b := []byte("test")
			pool.Push(b)

			g.Assert(MutexLocked(&pool.mu)).IsFalse()
			g.Assert(<-ch1).Equal(b)
			g.Assert(<-ch2).Equal(b)
			g.Assert(len(pool.sinks)).Equal(2)
		})

		g.It("does not block if a channel is nil or otherwise full", func() {
			ch := make([]chan []byte, 2)
			ch[1] = make(chan []byte, 1)
			ch[1] <- []byte("test")

			pool.On(ch[0])
			pool.On(ch[1])

			pool.Push([]byte("testing"))

			g.Assert(MutexLocked(&pool.mu)).IsFalse()
			g.Assert(<-ch[1]).Equal([]byte("test"))

			pool.Push([]byte("test2"))
			g.Assert(<-ch[1]).Equal([]byte("test2"))
			g.Assert(MutexLocked(&pool.mu)).IsFalse()
		})
	})

	g.Describe("SinkPool#Destroy", func() {
		var pool *sinkPool
		g.BeforeEach(func() {
			pool = &sinkPool{}
		})

		g.It("works if no sinks are registered", func() {
			pool.Destroy()

			g.Assert(MutexLocked(&pool.mu)).IsFalse()
		})

		g.It("closes all channels fully", func() {
			ch1 := make(chan []byte, 1)
			ch2 := make(chan []byte, 1)

			pool.On(ch1)
			pool.On(ch2)

			g.Assert(len(pool.sinks)).Equal(2)
			pool.Destroy()
			g.Assert(pool.sinks).IsZero()

			defer func() {
				r := recover()

				g.Assert(r).IsNotNil()
				g.Assert(r.(error).Error()).Equal("send on closed channel")
			}()

			ch1 <- []byte("test")
		})

		g.It("works when a sink channel is nil", func() {
			ch := make([]chan []byte, 2)

			pool.On(ch[0])
			pool.On(ch[1])

			pool.Destroy()

			g.Assert(MutexLocked(&pool.mu)).IsFalse()
		})
	})
}