package server

import (
	"context"
	"testing"
	"time"

	"emperror.dev/errors"
	. "github.com/franela/goblin"
)

func TestPower(t *testing.T) {
	g := Goblin(t)

	g.Describe("PowerLocker", func() {
		var pl *powerLocker
		g.BeforeEach(func() {
			pl = newPowerLocker()
		})

		g.Describe("PowerLocker#IsLocked", func() {
			g.It("should return false when the channel is empty", func() {
				g.Assert(cap(pl.ch)).Equal(1)
				g.Assert(pl.IsLocked()).IsFalse()
			})

			g.It("should return true when the channel is at capacity", func() {
				pl.ch <- true

				g.Assert(pl.IsLocked()).IsTrue()
				<-pl.ch
				g.Assert(pl.IsLocked()).IsFalse()

				// We don't care what the channel value is, just that there is
				// something in it.
				pl.ch <- false
				g.Assert(pl.IsLocked()).IsTrue()
				g.Assert(cap(pl.ch)).Equal(1)
			})
		})

		g.Describe("PowerLocker#Acquire", func() {
			g.It("should acquire a lock when channel is empty", func() {
				err := pl.Acquire()

				g.Assert(err).IsNil()
				g.Assert(cap(pl.ch)).Equal(1)
				g.Assert(len(pl.ch)).Equal(1)
			})

			g.It("should return an error when the channel is full", func() {
				pl.ch <- true

				err := pl.Acquire()

				g.Assert(err).IsNotNil()
				g.Assert(errors.Is(err, ErrPowerLockerLocked)).IsTrue()
				g.Assert(cap(pl.ch)).Equal(1)
				g.Assert(len(pl.ch)).Equal(1)
			})
		})

		g.Describe("PowerLocker#TryAcquire", func() {
			g.It("should acquire a lock when channel is empty", func() {
				g.Timeout(time.Second)

				err := pl.TryAcquire(context.Background())

				g.Assert(err).IsNil()
				g.Assert(cap(pl.ch)).Equal(1)
				g.Assert(len(pl.ch)).Equal(1)
				g.Assert(pl.IsLocked()).IsTrue()
			})

			g.It("should block until context is canceled if channel is full", func() {
				g.Timeout(time.Second)
				ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond*500)
				defer cancel()

				pl.ch <- true
				err := pl.TryAcquire(ctx)

				g.Assert(err).IsNotNil()
				g.Assert(errors.Is(err, context.DeadlineExceeded)).IsTrue()
				g.Assert(cap(pl.ch)).Equal(1)
				g.Assert(len(pl.ch)).Equal(1)
				g.Assert(pl.IsLocked()).IsTrue()
			})

			g.It("should block until lock can be acquired", func() {
				g.Timeout(time.Second)

				ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond*200)
				defer cancel()

				pl.Acquire()
				go func() {
					time.AfterFunc(time.Millisecond * 50, func() {
						pl.Release()
					})
				}()

				err := pl.TryAcquire(ctx)
				g.Assert(err).IsNil()
				g.Assert(cap(pl.ch)).Equal(1)
				g.Assert(len(pl.ch)).Equal(1)
				g.Assert(pl.IsLocked()).IsTrue()
			})
		})

		g.Describe("PowerLocker#Release", func() {
			g.It("should release when channel is full", func() {
				pl.Acquire()
				g.Assert(pl.IsLocked()).IsTrue()
				pl.Release()
				g.Assert(cap(pl.ch)).Equal(1)
				g.Assert(len(pl.ch)).Equal(0)
				g.Assert(pl.IsLocked()).IsFalse()
			})

			g.It("should release when channel is empty", func() {
				g.Assert(pl.IsLocked()).IsFalse()
				pl.Release()
				g.Assert(cap(pl.ch)).Equal(1)
				g.Assert(len(pl.ch)).Equal(0)
				g.Assert(pl.IsLocked()).IsFalse()
			})
		})

		g.Describe("PowerLocker#Destroy", func() {
			g.It("should unlock and close the channel", func() {
				pl.Acquire()
				g.Assert(pl.IsLocked()).IsTrue()
				pl.Destroy()
				g.Assert(pl.IsLocked()).IsFalse()

				defer func() {
					r := recover()

					g.Assert(r).IsNotNil()
					g.Assert(r.(error).Error()).Equal("send on closed channel")
				}()

				pl.Acquire()
			})
		})
	})

	g.Describe("Server#ExecutingPowerAction", func() {
		g.It("should return based on locker status", func() {
			s := &Server{powerLock: newPowerLocker()}

			g.Assert(s.ExecutingPowerAction()).IsFalse()
			s.powerLock.Acquire()
			g.Assert(s.ExecutingPowerAction()).IsTrue()
		})
	})
}
