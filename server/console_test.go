package server

import (
	"testing"
	"time"

	"github.com/franela/goblin"
)

func TestName(t *testing.T) {
	g := goblin.Goblin(t)

	g.Describe("ConsoleThrottler", func() {
		g.It("keeps count of the number of overages in a time period", func() {
			t := newConsoleThrottle(1, time.Second)
			g.Assert(t.Allow()).IsTrue()
			g.Assert(t.Allow()).IsFalse()
			g.Assert(t.Allow()).IsFalse()
		})

		g.It("calls strike once per time period", func() {
			t := newConsoleThrottle(1, time.Millisecond*20)

			var times int
			t.strike = func() {
				times = times + 1
			}

			t.Allow()
			t.Allow()
			t.Allow()
			time.Sleep(time.Millisecond * 100)
			t.Allow()
			t.Reset()
			t.Allow()
			t.Allow()
			t.Allow()

			g.Assert(times).Equal(2)
		})

		g.It("is properly reset", func() {
			t := newConsoleThrottle(10, time.Second)

			for i := 0; i < 10; i++ {
				g.Assert(t.Allow()).IsTrue()
			}
			g.Assert(t.Allow()).IsFalse()
			t.Reset()
			g.Assert(t.Allow()).IsTrue()
		})
	})
}

func BenchmarkConsoleThrottle(b *testing.B) {
	t := newConsoleThrottle(10, time.Millisecond*10)

	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		t.Allow()
	}
}
