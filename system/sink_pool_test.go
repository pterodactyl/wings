package system

import (
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"

	. "github.com/franela/goblin"
)

func MutexLocked(m *sync.RWMutex) bool {
	v := reflect.ValueOf(m).Elem()

	state := v.FieldByName("w").FieldByName("state")

	readerCountField := v.FieldByName("readerCount")
	// go1.20 changed readerCount to an atomic
	// ref; https://github.com/golang/go/commit/e509452727b469d89a3fc4a7d1cbf9d3f110efee
	var readerCount int64
	if readerCountField.Kind() == reflect.Struct {
		readerCount = readerCountField.FieldByName("v").Int()
	} else {
		readerCount = readerCountField.Int()
	}
	return state.Int()&1 == 1 || readerCount > 0
}

func TestSink(t *testing.T) {
	g := Goblin(t)

	g.Describe("SinkPool#On", func() {
		g.It("pushes additional channels to a sink", func() {
			pool := &SinkPool{}

			g.Assert(pool.sinks).IsZero()

			c1 := make(chan []byte, 1)
			pool.On(c1)

			g.Assert(len(pool.sinks)).Equal(1)
			g.Assert(MutexLocked(&pool.mu)).IsFalse()
		})
	})

	g.Describe("SinkPool#Off", func() {
		var pool *SinkPool
		g.BeforeEach(func() {
			pool = &SinkPool{}
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

			defer func() {
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
		var pool *SinkPool
		g.BeforeEach(func() {
			pool = &SinkPool{}
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

		g.It("uses a ring-buffer to avoid blocking when the channel is full", func() {
			ch1 := make(chan []byte, 1)
			ch2 := make(chan []byte, 2)
			ch3 := make(chan []byte)

			// ch1 and ch2 are now full, and would block if the code doesn't account
			// for a full buffer.
			ch1 <- []byte("pre-test")
			ch2 <- []byte("pre-test")
			ch2 <- []byte("pre-test 2")

			pool.On(ch1)
			pool.On(ch2)
			pool.On(ch3)

			pool.Push([]byte("testing"))
			time.Sleep(time.Millisecond * 20)

			g.Assert(MutexLocked(&pool.mu)).IsFalse()
			// We expect that value previously in the channel to have been dumped
			// and therefore only the value we pushed will be present. For ch2 we
			// expect only the first message was dropped, and the second one is now
			// the first in the out queue.
			g.Assert(<-ch1).Equal([]byte("testing"))
			g.Assert(<-ch2).Equal([]byte("pre-test 2"))
			g.Assert(<-ch2).Equal([]byte("testing"))

			// Because nothing in this test was listening for ch3, it would have
			// blocked for the 10ms duration, and then been skipped over entirely
			// because it had no length to try and push onto.
			g.Assert(len(ch3)).Equal(0)

			// Now, push again and expect similar results.
			pool.Push([]byte("testing 2"))
			time.Sleep(time.Millisecond * 20)

			g.Assert(MutexLocked(&pool.mu)).IsFalse()
			g.Assert(<-ch1).Equal([]byte("testing 2"))
			g.Assert(<-ch2).Equal([]byte("testing 2"))
		})

		g.It("can handle concurrent pushes FIFO", func() {
			ch := make(chan []byte, 4)

			pool.On(ch)
			pool.On(make(chan []byte))

			for i := 0; i < 100; i++ {
				pool.Push([]byte(fmt.Sprintf("iteration %d", i)))
			}

			time.Sleep(time.Millisecond * 20)
			g.Assert(MutexLocked(&pool.mu)).IsFalse()
			g.Assert(len(ch)).Equal(4)

			g.Timeout(time.Millisecond * 500)
			g.Assert(<-ch).Equal([]byte("iteration 96"))
			g.Assert(<-ch).Equal([]byte("iteration 97"))
			g.Assert(<-ch).Equal([]byte("iteration 98"))
			g.Assert(<-ch).Equal([]byte("iteration 99"))
			g.Assert(len(ch)).Equal(0)
		})
	})

	g.Describe("SinkPool#Destroy", func() {
		var pool *SinkPool
		g.BeforeEach(func() {
			pool = &SinkPool{}
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
