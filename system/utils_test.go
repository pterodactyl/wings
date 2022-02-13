package system

import (
	"math/rand"
	"strings"
	"sync"
	"testing"
	"time"

	. "github.com/franela/goblin"
	"github.com/goccy/go-json"
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

	g.Describe("AtomicBool", func() {
		var b *AtomicBool
		g.BeforeEach(func() {
			b = NewAtomicBool(false)
		})

		g.It("initalizes with the provided start value", func() {
			b = NewAtomicBool(true)
			g.Assert(b.Load()).IsTrue()

			b = NewAtomicBool(false)
			g.Assert(b.Load()).IsFalse()
		})

		g.Describe("AtomicBool#Store", func() {
			g.It("stores the provided value", func() {
				g.Assert(b.Load()).IsFalse()
				b.Store(true)
				g.Assert(b.Load()).IsTrue()
			})

			// This test makes no assertions, it just expects to not hit a race condition
			// by having multiple things writing at the same time.
			g.It("handles contention from multiple routines", func() {
				var wg sync.WaitGroup

				wg.Add(100)
				for i := 0; i < 100; i++ {
					go func(i int) {
						b.Store(i%2 == 0)
						wg.Done()
					}(i)
				}
				wg.Wait()
			})
		})

		g.Describe("AtomicBool#SwapIf", func() {
			g.It("swaps the value out if different than what is stored", func() {
				o := b.SwapIf(false)
				g.Assert(o).IsFalse()
				g.Assert(b.Load()).IsFalse()

				o = b.SwapIf(true)
				g.Assert(o).IsTrue()
				g.Assert(b.Load()).IsTrue()

				o = b.SwapIf(true)
				g.Assert(o).IsFalse()
				g.Assert(b.Load()).IsTrue()

				o = b.SwapIf(false)
				g.Assert(o).IsTrue()
				g.Assert(b.Load()).IsFalse()
			})
		})

		g.Describe("can be marshaled with JSON", func() {
			type testStruct struct {
				Value AtomicBool `json:"value"`
			}

			var o testStruct
			err := json.Unmarshal([]byte(`{"value":true}`), &o)

			g.Assert(err).IsNil()
			g.Assert(o.Value.Load()).IsTrue()

			b, err2 := json.Marshal(&o)
			g.Assert(err2).IsNil()
			g.Assert(b).Equal([]byte(`{"value":true}`))
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
