package filesystem

import (
	"io"
	"testing"

	"emperror.dev/errors"
	. "github.com/franela/goblin"
)

type stackTracer interface {
	StackTrace() errors.StackTrace
}

func TestFilesystem_PathResolutionError(t *testing.T) {
	g := Goblin(t)

	g.Describe("NewFilesystemError", func() {
		g.It("includes a stack trace for the error", func() {
			err := newFilesystemError(ErrCodeUnknownError, nil)

			_, ok := err.(stackTracer)
			g.Assert(ok).IsTrue()
		})

		g.It("properly wraps the underlying error cause", func() {
			underlying := io.EOF
			err := newFilesystemError(ErrCodeUnknownError, underlying)

			_, ok := err.(stackTracer)
			g.Assert(ok).IsTrue()

			_, ok = err.(*Error)
			g.Assert(ok).IsFalse()

			fserr, ok := errors.Unwrap(err).(*Error)
			g.Assert(ok).IsTrue()
			g.Assert(fserr.Unwrap()).IsNotNil()
			g.Assert(fserr.Unwrap()).Equal(underlying)
		})
	})

	g.Describe("NewBadPathResolutionError", func() {
		g.It("is can detect itself as an error correctly", func() {
			err := NewBadPathResolution("foo", "bar")
			g.Assert(IsErrorCode(err, ErrCodePathResolution)).IsTrue()
			g.Assert(err.Error()).Equal("filesystem: server path [foo] resolves to a location outside the server root: bar")
			g.Assert(IsErrorCode(&Error{code: ErrCodeIsDirectory}, ErrCodePathResolution)).IsFalse()
		})

		g.It("returns <empty> if no destination path is provided", func() {
			err := NewBadPathResolution("foo", "")
			g.Assert(err).IsNotNil()
			g.Assert(err.Error()).Equal("filesystem: server path [foo] resolves to a location outside the server root: <empty>")
		})
	})
}
