package progress_test

import (
	"bytes"
	"testing"

	"github.com/franela/goblin"

	"github.com/pterodactyl/wings/internal/progress"
)

func TestProgress(t *testing.T) {
	g := goblin.Goblin(t)

	g.Describe("Progress", func() {
		g.It("properly initializes", func() {
			total := uint64(1000)
			p := progress.NewProgress(total)
			g.Assert(p).IsNotNil()
			g.Assert(p.Total()).Equal(total)
			g.Assert(p.Written()).Equal(uint64(0))
		})

		g.It("increments written when Write is called", func() {
			v := []byte("hello")
			p := progress.NewProgress(1000)
			_, err := p.Write(v)
			g.Assert(err).IsNil()
			g.Assert(p.Written()).Equal(uint64(len(v)))
		})

		g.It("renders a progress bar", func() {
			v := bytes.Repeat([]byte{' '}, 100)
			p := progress.NewProgress(1000)
			_, err := p.Write(v)
			g.Assert(err).IsNil()
			g.Assert(p.Written()).Equal(uint64(len(v)))
			g.Assert(p.Progress(25)).Equal("[==                       ] 100 B / 1000 B")
		})

		g.It("renders a progress bar when written exceeds total", func() {
			v := bytes.Repeat([]byte{' '}, 1001)
			p := progress.NewProgress(1000)
			_, err := p.Write(v)
			g.Assert(err).IsNil()
			g.Assert(p.Written()).Equal(uint64(len(v)))
			g.Assert(p.Progress(25)).Equal("[=========================] 1001 B / 1000 B")
		})
	})
}
