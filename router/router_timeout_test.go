package router

import (
	"bufio"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pterodactyl/wings/config"
	"github.com/pterodactyl/wings/server"
)

func startTimeoutTestServer(t *testing.T) string {
	t.Helper()
	addr, _ := startTimeoutTestServerWithClosed(t)
	return addr
}

// startTimeoutTestServerWithClosed starts the server as it is configured for
// Wings, with shorter timeouts, also returning the number of connections the
// server has closed.
func startTimeoutTestServerWithClosed(t *testing.T) (string, *atomic.Int64) {
	t.Helper()
	t.Setenv("WINGS_TOKEN_ID", "")
	t.Setenv("WINGS_TOKEN", "")

	prh, pit, pur := ReadHeaderTimeout, IdleTimeout, unauthenticatedTimeout
	ReadHeaderTimeout, IdleTimeout, unauthenticatedTimeout = time.Millisecond*300, time.Millisecond*300, time.Millisecond*500
	t.Cleanup(func() {
		ReadHeaderTimeout, IdleTimeout, unauthenticatedTimeout = prh, pit, pur
	})

	cfg, err := config.NewAtPath(filepath.Join(t.TempDir(), "config.yml"))
	if err != nil {
		t.Fatal(err)
	}
	cfg.AuthenticationTokenId = "test-id"
	cfg.AuthenticationToken = "test-token"
	if err := cfg.ResolveToken(false); err != nil {
		t.Fatal(err)
	}
	config.Set(cfg)

	manager := server.NewEmptyManager(backupTestRemoteClient{credentials: make(chan [2]string, 1)})
	srv := httptest.NewUnstartedServer(nil)
	srv.Config = NewServer("", Configure(manager, nil), nil)
	var closed atomic.Int64
	srv.Config.ConnState = func(_ net.Conn, st http.ConnState) {
		if st == http.StateClosed {
			closed.Add(1)
		}
	}
	srv.Start()
	t.Cleanup(srv.Close)

	return strings.TrimPrefix(srv.URL, "http://"), &closed
}

// Requests have a deadline whichever way they are routed.
func TestSlowRequestBodiesAreTimedOutBeforeRouting(t *testing.T) {
	requests := map[string]string{
		"redirect": "POST /upload/file/ HTTP/1.1\r\nHost: wings\r\nContent-Length: 1000\r\n\r\n",
		"chunked":  "POST /api/servers/ HTTP/1.1\r\nHost: wings\r\nTransfer-Encoding: chunked\r\n\r\n",
		"options":  "OPTIONS * HTTP/1.1\r\nHost: wings\r\nContent-Length: 1000\r\n\r\n",
	}
	for name, head := range requests {
		t.Run(name, func(t *testing.T) {
			addr := startTimeoutTestServer(t)
			conn, err := net.Dial("tcp", addr)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()

			if _, err := conn.Write([]byte(head)); err != nil {
				t.Fatal(err)
			}
			body := strings.Repeat("a", 1000)
			if strings.Contains(head, "chunked") {
				body = strings.Repeat("1\r\na\r\n", 200)
			}
			go trickle(conn, body, time.Millisecond*50)
			if err := waitForClose(conn, time.Second*3); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// Connections whose responses are not read are closed.
func TestSlowResponseReadsAreTimedOut(t *testing.T) {
	addr, closed := startTimeoutTestServerWithClosed(t)
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.(*net.TCPConn).SetReadBuffer(4096)

	// Enough responses to fill the socket buffers, which are never read.
	go func() {
		req := []byte("GET /api/system HTTP/1.1\r\nHost: wings\r\n\r\n")
		for i := 0; i < 40000; i++ {
			if _, err := conn.Write(req); err != nil {
				return
			}
		}
	}()

	deadline := time.Now().Add(time.Second * 5)
	for closed.Load() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("connection was not closed by the server")
		}
		time.Sleep(time.Millisecond * 50)
	}
}

// trickle writes the data to the connection one byte at a time.
func trickle(conn net.Conn, data string, interval time.Duration) {
	for i := 0; i < len(data); i++ {
		if _, err := conn.Write([]byte{data[i]}); err != nil {
			return
		}
		time.Sleep(interval)
	}
}

// waitForClose reads from the connection until the server closes it, returning
// an error if that does not happen in time.
func waitForClose(conn net.Conn, timeout time.Duration) error {
	_ = conn.SetReadDeadline(time.Now().Add(timeout))
	_, err := io.Copy(io.Discard, conn)
	if errors.Is(err, os.ErrDeadlineExceeded) {
		return errors.New("connection was not closed by the server")
	}
	return nil
}

func TestSlowRequestHeadersAreTimedOut(t *testing.T) {
	addr := startTimeoutTestServer(t)
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	go trickle(conn, "GET /api/system HTTP/1.1\r\nHost: wings\r\nX-Slow: "+strings.Repeat("a", 1000), time.Millisecond*50)
	if err := waitForClose(conn, time.Second*3); err != nil {
		t.Fatal(err)
	}
}

func TestSlowUnauthenticatedRequestBodyIsTimedOut(t *testing.T) {
	addr := startTimeoutTestServer(t)
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	if _, err := conn.Write([]byte("POST /api/update HTTP/1.1\r\nHost: wings\r\nContent-Type: application/json\r\nContent-Length: 1000\r\n\r\n")); err != nil {
		t.Fatal(err)
	}
	go trickle(conn, strings.Repeat(" ", 1000), time.Millisecond*50)
	if err := waitForClose(conn, time.Second*3); err != nil {
		t.Fatal(err)
	}
}

func TestSlowAuthenticatedRequestBodyIsAllowed(t *testing.T) {
	addr := startTimeoutTestServer(t)
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	body := `{"token_id":"test-id","token":"test-token"}`
	if _, err := conn.Write([]byte("POST /api/update HTTP/1.1\r\nHost: wings\r\nAuthorization: Bearer test-token\r\nContent-Type: application/json\r\nContent-Length: " + strconv.Itoa(len(body)) + "\r\n\r\n")); err != nil {
		t.Fatal(err)
	}
	// Takes well over the deadline for unauthenticated requests to send.
	start := time.Now()
	trickle(conn, body, time.Millisecond*30)
	if time.Since(start) < unauthenticatedTimeout*2 {
		t.Fatal("expected the body to take longer than the unauthenticated read timeout to send")
	}

	_ = conn.SetReadDeadline(time.Now().Add(time.Second * 3))
	res, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusOK {
		t.Fatalf("expected authenticated request to succeed, got status %d", res.StatusCode)
	}
}
