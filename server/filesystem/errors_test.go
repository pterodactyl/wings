package filesystem

import (
	. "github.com/franela/goblin"
	"testing"
)

func TestFilesystem_PathResolutionError(t *testing.T) {
	g := Goblin(t)

	g.Describe("NewBadPathResolutionError", func() {
		g.It("is can detect itself as an error correctly", func() {
			err := NewBadPathResolution("foo", "bar")
			g.Assert(IsErrorCode(err, ErrCodePathResolution)).IsTrue()
			g.Assert(err.Error()).Equal("filesystem: server path [foo] resolves to a location outside the server root: bar")
			g.Assert(IsErrorCode(&Error{code: ErrCodeIsDirectory}, ErrCodePathResolution)).IsFalse()
		})

		g.It("returns <empty> if no destination path is provided", func() {
			err := NewBadPathResolution("foo", "")
			g.Assert(err.Error()).Equal("filesystem: server path [foo] resolves to a location outside the server root: <empty>")
		})
	})
}
