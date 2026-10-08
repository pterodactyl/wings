package downloader

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pterodactyl/wings/config"
	"github.com/pterodactyl/wings/server"
)

func TestMain(m *testing.M) {
	config.Set(&config.Configuration{AuthenticationToken: "test-token"})
	os.Exit(m.Run())
}

// setAllowlist sets the remote download allowlist for the duration of the test.
func setAllowlist(t *testing.T, entries ...string) {
	t.Helper()
	previous := config.Get()
	t.Cleanup(func() { config.Set(previous) })
	next := *previous
	next.Api.RemoteDownloadAllowlist = entries
	config.Set(&next)
}

// countingListener counts the connections it accepts.
type countingListener struct {
	net.Listener
	accepted atomic.Int64
}

func (l *countingListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err == nil {
		l.accepted.Add(1)
	}
	return c, err
}

func TestDownloadDoesNotConnectToInternalAddresses(t *testing.T) {
	inner, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	l := &countingListener{Listener: inner}
	var requests atomic.Int64
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		_, _ = w.Write([]byte("internal"))
	})}
	go func() { _ = srv.Serve(l) }()
	t.Cleanup(func() { _ = srv.Close() })
	port := strconv.Itoa(inner.Addr().(*net.TCPAddr).Port)

	s, err := server.New(nil)
	if err != nil {
		t.Fatal(err)
	}

	// The shared address space and benchmarking ranges are not reachable on most
	// machines, but the download must be refused before any connection is made
	// whether or not they are.
	for _, host := range []string{"127.0.0.1", "[::1]", "[::ffff:127.0.0.1]", "100.64.0.5", "198.18.0.5", "0.0.0.0"} {
		t.Run(host, func(t *testing.T) {
			u, _ := url.Parse("http://" + host + ":" + port + "/file")
			err := New(s, DownloadRequest{Directory: "/", URL: u, FileName: "file"}).Execute()
			if !errors.Is(err, ErrInternalResolution) {
				t.Fatalf("expected download from %s to be refused, got %v", host, err)
			}
		})
	}

	// Give any connection that was made, and any request sent over it, time to
	// arrive before checking.
	time.Sleep(time.Millisecond * 100)
	if n := l.accepted.Load(); n != 0 {
		t.Fatalf("expected no connections to internal addresses, got %d", n)
	}
	if n := requests.Load(); n != 0 {
		t.Fatalf("expected no requests to internal addresses, got %d", n)
	}
}

// The address is checked again immediately before connecting, since the host is
// resolved again when dialing and could resolve to an internal address then.
func TestCheckConnectionRefusesInternalAddresses(t *testing.T) {
	for _, addr := range []string{"127.0.0.1:80", "[::1]:80", "10.0.0.1:443", "100.64.0.1:80", "[fe80::1%lo]:80", "[64:ff9b::7f00:1]:80"} {
		if err := checkConnection(context.Background(), "tcp", addr, nil); !errors.Is(err, ErrInternalResolution) {
			t.Errorf("expected connection to %s to be refused, got %v", addr, err)
		}
	}
	for _, addr := range []string{"1.1.1.1:443", "[2606:4700:4700::1111]:443"} {
		if err := checkConnection(context.Background(), "tcp", addr, nil); err != nil {
			t.Errorf("expected connection to %s to be allowed, got %v", addr, err)
		}
	}
}

// Operators can allow destinations that are otherwise refused, such as services
// on a Tailscale network or a cloud provider's internal endpoints.
func TestRemoteDownloadAllowlist(t *testing.T) {
	setAllowlist(t, "100.64.0.0/10", "internal.example.", "127.0.0.1")

	host := func(h string) context.Context { return context.WithValue(context.Background(), dialHostKey{}, h) }
	tests := []struct {
		ctx     context.Context
		addr    string
		allowed bool
	}{
		{context.Background(), "100.64.0.1:80", true},
		{context.Background(), "100.127.255.254:443", true},
		{context.Background(), "10.0.0.1:80", false},
		{host("internal.example"), "10.0.0.1:80", true},
		{host("other.example"), "10.0.0.1:80", false},
		{context.Background(), "1.1.1.1:443", true},
	}
	for _, tt := range tests {
		err := checkConnection(tt.ctx, "tcp", tt.addr, nil)
		if tt.allowed && err != nil {
			t.Errorf("expected connection to %s to be allowed, got %v", tt.addr, err)
		} else if !tt.allowed && !errors.Is(err, ErrInternalResolution) {
			t.Errorf("expected connection to %s to be refused, got %v", tt.addr, err)
		}
	}

	// A request to an allowed internal address is made through the client used
	// for downloads.
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("allowed"))
	})}
	go func() { _ = srv.Serve(l) }()
	t.Cleanup(func() { _ = srv.Close() })
	res, err := client.Get("http://" + l.Addr().String() + "/file")
	if err != nil {
		t.Fatalf("expected a request to an allowed address to succeed, got %v", err)
	}
	b, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if string(b) != "allowed" {
		t.Fatalf("expected the response from the allowed address, got %q", b)
	}
}

// Hosts that cannot be resolved are refused.
func TestDownloadRefusesUnresolvableHosts(t *testing.T) {
	s, err := server.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse("http://does-not-exist.invalid/file")
	if err := New(s, DownloadRequest{Directory: "/", URL: u, FileName: "file"}).Execute(); !IsDownloadError(err) {
		t.Fatalf("expected an unresolvable host to be refused, got %v", err)
	}
}

// Downloads from internal addresses are refused, whether or not anything is
// listening on the port.
func TestDownloadsFromInternalPortsAreRefused(t *testing.T) {
	var wg sync.WaitGroup
	open, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer open.Close()
	wg.Add(1)
	go func() {
		defer wg.Done()
		if c, err := open.Accept(); err == nil {
			_ = c.Close()
		}
	}()
	closed, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	closedAddr := closed.Addr().String()
	_ = closed.Close()

	s, err := server.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	var errs []error
	for _, addr := range []string{open.Addr().String(), closedAddr} {
		u, _ := url.Parse("http://" + addr + "/file")
		errs = append(errs, New(s, DownloadRequest{Directory: "/", URL: u, FileName: "file"}).Execute())
	}
	for _, err := range errs {
		if !errors.Is(err, ErrInternalResolution) {
			t.Fatalf("expected downloads from open and closed ports to be refused, got %v", errs)
		}
	}
	_ = open.Close()
	wg.Wait()
}
