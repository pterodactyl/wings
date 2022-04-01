package system

import (
	"context"
	"testing"
	"time"

	"emperror.dev/errors"
	. "github.com/franela/goblin"
)

func TestPower(t *testing.T) {
	g := Goblin(t)

	g.Describe("Locker", func() {
		var l *Locker
		g.BeforeEach(func() {
			l = NewLocker()
		})

		g.Describe("PowerLocker#IsLocked", func() {
			g.It("should return false when the channel is empty", func() {
				g.Assert(cap(l.ch)).Equal(1)
				g.Assert(l.IsLocked()).IsFalse()
			})

			g.It("should return true when the channel is at capacity", func() {
				l.ch <- true

				g.Assert(l.IsLocked()).IsTrue()
				<-l.ch
				g.Assert(l.IsLocked()).IsFalse()

				// We don't care what the channel value is, just that there is
				// something in it.
				l.ch <- false
				g.Assert(l.IsLocked()).IsTrue()
				g.Assert(cap(l.ch)).Equal(1)
			})
		})

		g.Describe("PowerLocker#Acquire", func() {
			g.It("should acquire a lock when channel is empty", func() {
				err := l.Acquire()

				g.Assert(err).IsNil()
				g.Assert(cap(l.ch)).Equal(1)
				g.Assert(len(l.ch)).Equal(1)
			})

			g.It("should return an error when the channel is full", func() {
				l.ch <- true

				err := l.Acquire()

				g.Assert(err).IsNotNil()
				g.Assert(errors.Is(err, ErrLockerLocked)).IsTrue()
				g.Assert(cap(l.ch)).Equal(1)
				g.Assert(len(l.ch)).Equal(1)
			})
		})

		g.Describe("PowerLocker#TryAcquire", func() {
			g.It("should acquire a lock when channel is empty", func() {
				g.Timeout(time.Second)

				err := l.TryAcquire(context.Background())

				g.Assert(err).IsNil()
				g.Assert(cap(l.ch)).Equal(1)
				g.Assert(len(l.ch)).Equal(1)
				g.Assert(l.IsLocked()).IsTrue()
			})

			g.It("should block until context is canceled if channel is full", func() {
				g.Timeout(time.Second)
				ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond*500)
				defer cancel()

				l.ch <- true
				err := l.TryAcquire(ctx)

				g.Assert(err).IsNotNil()
				g.Assert(errors.Is(err, ErrLockerLocked)).IsTrue()
				g.Assert(cap(l.ch)).Equal(1)
				g.Assert(len(l.ch)).Equal(1)
				g.Assert(l.IsLocked()).IsTrue()
			})

			g.It("should block until lock can be acquired", func() {
				g.Timeout(time.Second)

				ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond*200)
				defer cancel()

				l.Acquire()
				go func() {
					time.AfterFunc(time.Millisecond*50, func() {
						l.Release()
					})
				}()

				err := l.TryAcquire(ctx)
				g.Assert(err).IsNil()
				g.Assert(cap(l.ch)).Equal(1)
				g.Assert(len(l.ch)).Equal(1)
				g.Assert(l.IsLocked()).IsTrue()
			})
		})

		g.Describe("PowerLocker#Release", func() {
			g.It("should release when channel is full", func() {
				l.Acquire()
				g.Assert(l.IsLocked()).IsTrue()
				l.Release()
				g.Assert(cap(l.ch)).Equal(1)
				g.Assert(len(l.ch)).Equal(0)
				g.Assert(l.IsLocked()).IsFalse()
			})

			g.It("should release when channel is empty", func() {
				g.Assert(l.IsLocked()).IsFalse()
				l.Release()
				g.Assert(cap(l.ch)).Equal(1)
				g.Assert(len(l.ch)).Equal(0)
				g.Assert(l.IsLocked()).IsFalse()
			})
		})

		g.Describe("PowerLocker#Destroy", func() {
			g.It("should unlock and close the channel", func() {
				l.Acquire()
				g.Assert(l.IsLocked()).IsTrue()
				l.Destroy()
				g.Assert(l.IsLocked()).IsFalse()

				defer func() {
					r := recover()

					g.Assert(r).IsNotNil()
					g.Assert(r.(error).Error()).Equal("send on closed channel")
				}()

				l.Acquire()
			})
		})
	})
}
