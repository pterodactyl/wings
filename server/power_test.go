package server

import (
	"testing"

	. "github.com/franela/goblin"
	"github.com/pterodactyl/wings/system"
)

func TestPower(t *testing.T) {
	g := Goblin(t)

	g.Describe("Server#ExecutingPowerAction", func() {
		g.It("should return based on locker status", func() {
			s := &Server{powerLock: system.NewLocker()}

			g.Assert(s.ExecutingPowerAction()).IsFalse()
			s.powerLock.Acquire()
			g.Assert(s.ExecutingPowerAction()).IsTrue()
		})
	})
}
