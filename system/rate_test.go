package system

import (
	"testing"
	"time"

	. "github.com/franela/goblin"
)

func TestRate(t *testing.T) {
	g := Goblin(t)

	g.Describe("Rate", func() {
		g.It("properly rate limits a bucket", func() {
			r := NewRate(10, time.Millisecond*100)

			for i := 0; i < 100; i++ {
				ok := r.Try()
				if i < 10 && !ok {
					g.Failf("should not have allowed take on try %d", i)
				} else if i >= 10 && ok {
					g.Failf("should have blocked take on try %d", i)
				}
			}
		})

		g.It("handles rate limiting in chunks", func() {
			var out []int
			r := NewRate(12, time.Millisecond*10)

			for i := 0; i < 100; i++ {
				if i%20 == 0 {
					// Give it time to recover.
					time.Sleep(time.Millisecond * 10)
				}
				if r.Try() {
					out = append(out, i)
				}
			}

			g.Assert(len(out)).Equal(60)
			g.Assert(out[0]).Equal(0)
			g.Assert(out[12]).Equal(20)
			g.Assert(out[len(out)-1]).Equal(91)
		})

		g.It("resets back to zero when called", func() {
			r := NewRate(10, time.Second)
			for i := 0; i < 100; i++ {
				if i%10 == 0 {
					r.Reset()
				}
				g.Assert(r.Try()).IsTrue()
			}
			g.Assert(r.Try()).IsFalse("final attempt should not allow taking")
		})
	})
}

func BenchmarkRate_Try(b *testing.B) {
	r := NewRate(10, time.Millisecond*100)

	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		r.Try()
	}
}
