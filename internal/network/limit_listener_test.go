package network

import (
	"net"
	"strings"
	"testing"
	"time"

	"github.com/apex/log"
	"github.com/apex/log/handlers/memory"
)

// Connections waiting on the limit are otherwise not logged at all, which left
// operators with nothing to go on when a node stopped accepting connections.
func TestLimitListenerWarnsWhenTheLimitIsReached(t *testing.T) {
	handler := memory.New()
	logger := log.Log.(*log.Logger)
	previous := logger.Handler
	logger.Handler = handler
	t.Cleanup(func() { logger.Handler = previous })

	inner, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	l := LimitListener(inner, 2, "test").(*limitListener)
	defer l.Close()

	var accepted []net.Conn
	for i := 0; i < 2; i++ {
		client, err := net.Dial("tcp", l.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		defer client.Close()
		c, err := l.Accept()
		if err != nil {
			t.Fatal(err)
		}
		accepted = append(accepted, c)
		if i == 0 && len(handler.Entries) != 0 {
			t.Fatalf("expected no warning below the limit, got %q", handler.Entries[0].Message)
		}
	}
	if len(handler.Entries) != 1 || !strings.Contains(handler.Entries[0].Message, "test: reached the maximum number of open connections") {
		t.Fatalf("expected one warning once the limit was reached, got %v", handler.Entries)
	}

	// Closing a connection more than once only frees its slot once.
	_ = accepted[0].Close()
	_ = accepted[0].Close()
	if n := l.open.Load(); n != 1 {
		t.Fatalf("expected one open connection, got %d", n)
	}

	// The connection that was closed makes room for another one.
	client, err := net.Dial("tcp", l.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	done := make(chan net.Conn, 1)
	go func() {
		c, _ := l.Accept()
		done <- c
	}()
	select {
	case c := <-done:
		_ = c.Close()
	case <-time.After(5 * time.Second):
		t.Fatal("expected a connection to be accepted after another was closed")
	}
	// The warning is only logged once a minute.
	if len(handler.Entries) != 1 {
		t.Fatalf("expected the warning to not be repeated within a minute, got %d entries", len(handler.Entries))
	}
}
