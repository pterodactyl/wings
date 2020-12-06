package cli

import (
	"fmt"
	"github.com/apex/log"
	"github.com/apex/log/handlers/cli"
	color2 "github.com/fatih/color"
	"github.com/mattn/go-colorable"
	"github.com/pkg/errors"
	"io"
	"math"
	"os"
	"sync"
	"time"
)

var Default = New(os.Stderr, true)
var bold = color2.New(color2.Bold)
var boldred = color2.New(color2.Bold, color2.FgRed)

var Strings = [...]string{
	log.DebugLevel: "DEBUG",
	log.InfoLevel:  " INFO",
	log.WarnLevel:  " WARN",
	log.ErrorLevel: "ERROR",
	log.FatalLevel: "FATAL",
}

type Handler struct {
	mu      sync.Mutex
	Writer  io.Writer
	Padding int
}

func New(w io.Writer, useColors bool) *Handler {
	if f, ok := w.(*os.File); ok {
		if useColors {
			return &Handler{Writer: colorable.NewColorable(f), Padding: 2}
		}
	}

	return &Handler{Writer: colorable.NewNonColorable(w), Padding: 2}
}

type tracer interface {
	StackTrace() errors.StackTrace
}

// HandleLog implements log.Handler.
func (h *Handler) HandleLog(e *log.Entry) error {
	color := cli.Colors[e.Level]
	level := Strings[e.Level]
	names := e.Fields.Names()

	h.mu.Lock()
	defer h.mu.Unlock()

	color.Fprintf(h.Writer, "%s: [%s] %-25s", bold.Sprintf("%*s", h.Padding+1, level), time.Now().Format(time.StampMilli), e.Message)

	for _, name := range names {
		if name == "source" {
			continue
		}
		fmt.Fprintf(h.Writer, " %s=%v", color.Sprint(name), e.Fields.Get(name))
	}

	fmt.Fprintln(h.Writer)

	for _, name := range names {
		if name != "error" {
			continue
		}
		if err, ok := e.Fields.Get("error").(error); ok {
			if e, ok := errors.Cause(err).(tracer); ok {
				st := e.StackTrace()
				l := math.Min(float64(len(st)), 10)
				fmt.Fprintf(h.Writer, "\n%s%+v\n\n", boldred.Sprintf("Stacktrace:"), st[0:int(l)])
			} else {
				fmt.Fprintf(h.Writer, "\n%s\n%+v\n\n", boldred.Sprintf("Stacktrace:"), err)
			}
		}
	}

	return nil
}
