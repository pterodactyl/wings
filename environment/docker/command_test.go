package docker

import (
	"net"
	"testing"
	"time"

	"github.com/docker/docker/api/types"

	"github.com/pterodactyl/wings/environment"
	"github.com/pterodactyl/wings/system"
)

// attachedEnvironment returns an environment attached to one end of a pipe,
// returning the other end, which receives the process input.
func attachedEnvironment(t *testing.T) (*Environment, net.Conn) {
	t.Helper()
	local, remote := net.Pipe()
	t.Cleanup(func() {
		_ = local.Close()
		_ = remote.Close()
	})
	e := &Environment{meta: &Metadata{}, st: system.NewAtomicString(environment.ProcessRunningState)}
	e.SetStream(&types.HijackedResponse{Conn: local})
	return e, remote
}

// Commands may not contain control characters, other than tabs.
func TestSendCommandRejectsControlCharacters(t *testing.T) {
	e, input := attachedEnvironment(t)
	received := make(chan string, 1)
	go func() {
		b := make([]byte, 64)
		n, _ := input.Read(b)
		received <- string(b[:n])
	}()

	for _, c := range []string{"\x10\x11", "say \x10", "\x03", "\x1b[A", "\x7f"} {
		if err := e.SendCommand(c); err == nil {
			t.Errorf("expected command %q to be rejected", c)
		}
	}
	if err := e.SendCommand("say\thello world"); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-received:
		if got != "say\thello world\n" {
			t.Fatalf("expected only the valid command to be sent, got %q", got)
		}
	case <-time.After(time.Second * 2):
		t.Fatal("expected the valid command to be sent")
	}
}

// Sending a command times out if the process does not read its input.
func TestSendCommandDoesNotBlockForever(t *testing.T) {
	previous := commandWriteTimeout
	commandWriteTimeout = time.Millisecond * 200
	t.Cleanup(func() { commandWriteTimeout = previous })

	e, _ := attachedEnvironment(t)
	done := make(chan error, 1)
	go func() { done <- e.SendCommand("say hello") }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected an error when the command could not be written")
		}
	case <-time.After(time.Second * 3):
		t.Fatal("expected sending the command to time out")
	}
}
