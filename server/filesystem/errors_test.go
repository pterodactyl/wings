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
}
