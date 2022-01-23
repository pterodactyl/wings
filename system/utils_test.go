package system

import (
	"math/rand"
	"strings"
	"testing"
	"time"

	. "github.com/franela/goblin"
)

func Test_Utils(t *testing.T) {
	g := Goblin(t)

	g.Describe("ScanReader", func() {
		g.BeforeEach(func() {
			maxBufferSize = 10
		})

		g.It("should truncate and return long lines", func() {
			reader := strings.NewReader("hello world this is a long line\nof text that should be truncated\nnot here\nbut definitely on this line")

			var lines []string
			err := ScanReader(reader, func(line []byte) {
				lines = append(lines, string(line))
			})

			g.Assert(err).IsNil()
			g.Assert(lines).Equal([]string{"hello worl", "of text th", "not here", "but defini"})
		})

		g.It("should replace cariage returns with newlines", func() {
			reader := strings.NewReader("test\rstring\r\nanother\rline\nhodor\r\r\rheld the door\nmaterial gourl\n")
			var lines []string
			err := ScanReader(reader, func(line []byte) {
				lines = append(lines, string(line))
			})

			g.Assert(err).IsNil()
			g.Assert(lines).Equal([]string{"test\rstrin", "another\rli", "hodor\r\r\rhe", "material g"})
		})
	})
}

func Benchmark_ScanReader(b *testing.B) {
	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	var str string
	for i := 0; i < 10; i++ {
		str += strings.Repeat("hello \rworld", r.Intn(2000)) + "\n"
	}
	reader := strings.NewReader(str)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = ScanReader(reader, func(line []byte) {
			// no op
		})
	}
}
