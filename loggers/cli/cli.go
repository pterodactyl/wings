package cli

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"emperror.dev/errors"
	"github.com/apex/log"
	"github.com/apex/log/handlers/cli"
	color2 "github.com/fatih/color"
	"github.com/mattn/go-colorable"
)

var (
	Default = New(os.Stderr, true)
	bold    = color2.New(color2.Bold)
	boldred = color2.New(color2.Bold, color2.FgRed)
)

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
			// Attach the stacktrace if it is missing at this point, but don't point
			// it specifically to this line since that is irrelevant.
			err = errors.WithStackDepthIf(err, 4)
			formatted := fmt.Sprintf("\n%s\n%+v\n\n", boldred.Sprintf("Stacktrace:"), err)

			if !strings.Contains(formatted, "runtime.goexit") {
				_, _ = fmt.Fprint(h.Writer, formatted)
				break
			}

			// Inserts a new-line between sections of a stack.
			// When wrapping errors, you get multiple separate stacks that start with their message,
			// this allows us to separate them with a new-line and view them more easily.
			//
			// For example:
			//
			// Stacktrace:
			// readlink test: no such file or directory
			// failed to read symlink target for 'test'
			// github.com/pterodactyl/wings/server/filesystem.(*Archive).addToArchive
			//         github.com/pterodactyl/wings/server/filesystem/archive.go:166
			// ... (Truncated the stack for easier reading)
			// runtime.goexit
			//         runtime/asm_amd64.s:1374
			// **NEW LINE INSERTED HERE**
			// backup: error while generating server backup
			// github.com/pterodactyl/wings/server.(*Server).Backup
			//         github.com/pterodactyl/wings/server/backup.go:84
			// ... (Truncated the stack for easier reading)
			// runtime.goexit
			//         runtime/asm_amd64.s:1374
			//
			var b strings.Builder
			var endOfStack bool
			for _, s := range strings.Split(formatted, "\n") {
				b.WriteString(s + "\n")

				if s == "runtime.goexit" {
					endOfStack = true
					continue
				}

				if !endOfStack {
					continue
				}

				b.WriteString("\n")
				endOfStack = false
			}

			_, _ = fmt.Fprint(h.Writer, b.String())
		}

		// Only one key with the name "error" can be in the map.
		break
	}

	return nil
}
