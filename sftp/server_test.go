package sftp

import (
	"bufio"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

func startTestServer(t *testing.T, limits connectionLimits) string {
	t.Helper()
	return startTestServerWithAuth(t, limits, func(ssh.ConnMetadata, []byte) (*ssh.Permissions, error) {
		return nil, errors.New("denied")
	})
}

func startTestServerWithAuth(t *testing.T, limits connectionLimits, auth func(ssh.ConnMetadata, []byte) (*ssh.Permissions, error)) string {
	t.Helper()
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(key)
	if err != nil {
		t.Fatal(err)
	}
	conf := &ssh.ServerConfig{PasswordCallback: auth}
	conf.AddHostKey(signer)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	srv := &SFTPServer{limits: limits}
	go func() {
		_ = srv.serve(listener, conf)
	}()
	return listener.Addr().String()
}

// dial opens a connection from the given loopback address and reports whether
// the server accepted it, which it signals by sending its version banner.
func dial(t *testing.T, addr string, from string) (net.Conn, bool) {
	t.Helper()
	d := net.Dialer{LocalAddr: &net.TCPAddr{IP: net.ParseIP(from)}}
	conn, err := d.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	_ = conn.SetReadDeadline(time.Now().Add(time.Second * 2))
	line, err := bufio.NewReader(conn).ReadString('\n')
	_ = conn.SetReadDeadline(time.Time{})
	return conn, err == nil && strings.HasPrefix(line, "SSH-")
}

func TestUnauthenticatedConnectionsAreClosedAfterTimeout(t *testing.T) {
	addr := startTestServer(t, connectionLimits{handshakeTimeout: time.Millisecond * 250, maxPending: 128, maxPendingPerIP: 16})

	conn, ok := dial(t, addr, "127.0.0.1")
	if !ok {
		t.Fatal("expected connection to be accepted")
	}

	// Never send anything; the server must close the connection on its own.
	start := time.Now()
	_ = conn.SetReadDeadline(time.Now().Add(time.Second * 5))
	_, err := conn.Read(make([]byte, 1))
	if err == nil || errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("expected the server to close an idle unauthenticated connection, got %v after %s", err, time.Since(start))
	}
}

func TestUnauthenticatedConnectionsAreLimited(t *testing.T) {
	addr := startTestServer(t, connectionLimits{handshakeTimeout: time.Minute, maxPending: 3, maxPendingPerIP: 2})

	// Two connections from one address are accepted, the third is not.
	first, ok := dial(t, addr, "127.0.0.1")
	if !ok {
		t.Fatal("expected first connection to be accepted")
	}
	if _, ok := dial(t, addr, "127.0.0.1"); !ok {
		t.Fatal("expected second connection to be accepted")
	}
	if _, ok := dial(t, addr, "127.0.0.1"); ok {
		t.Fatal("expected third connection from the same address to be dropped")
	}

	// Another address has its own limit, until the total limit is reached.
	if _, ok := dial(t, addr, "127.0.0.2"); !ok {
		t.Fatal("expected connection from another address to be accepted")
	}
	if _, ok := dial(t, addr, "127.0.0.3"); ok {
		t.Fatal("expected connection to be dropped once the total limit is reached")
	}

	// Closing a pending connection frees up its slot.
	_ = first.Close()
	deadline := time.Now().Add(time.Second * 5)
	for {
		if _, ok := dial(t, addr, "127.0.0.1"); ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("expected slot to be released when a pending connection closes")
		}
		time.Sleep(time.Millisecond * 50)
	}
}

// Every address in an IPv6 /64 network belongs to the same user, so they share a
// limit.
func TestConnectionKey(t *testing.T) {
	tests := map[string]string{
		"127.0.0.1:2022":                 "127.0.0.1",
		"[::ffff:10.0.0.1]:2022":         "10.0.0.1",
		"[2001:db8::1]:2022":             "2001:db8::/64",
		"[2001:db8::ffff:ffff]:2022":     "2001:db8::/64",
		"[2001:db8:0:1::1]:2022":         "2001:db8:0:1::/64",
		"[fe80::1%eth0]:2022":            "fe80::/64",
		"[2001:db8::abcd:1234:5678]:443": "2001:db8::/64",
	}
	for addr, want := range tests {
		a, err := net.ResolveTCPAddr("tcp", addr)
		if err != nil {
			t.Fatal(err)
		}
		if got := connectionKey(a); got != want {
			t.Errorf("connectionKey(%s) = %s, expected %s", addr, got, want)
		}
	}
}

// Authenticated connections are limited per user, so that a single user cannot
// use up every connection.
func TestAuthenticatedConnectionsAreLimitedPerUser(t *testing.T) {
	limits := defaultLimits
	limits.maxPerUser = 2
	addr := startTestServerWithAuth(t, limits, func(ssh.ConnMetadata, []byte) (*ssh.Permissions, error) {
		return &ssh.Permissions{}, nil
	})
	connect := func(user string) (*ssh.Client, error) {
		return ssh.Dial("tcp", addr, &ssh.ClientConfig{
			User:            user,
			Auth:            []ssh.AuthMethod{ssh.Password("password")},
			HostKeyCallback: ssh.InsecureIgnoreHostKey(),
			Timeout:         time.Second * 5,
		})
	}
	// closedWithin reports whether the server closes the connection in time.
	closedWithin := func(c *ssh.Client, d time.Duration) bool {
		done := make(chan struct{})
		go func() {
			_ = c.Wait()
			close(done)
		}()
		select {
		case <-done:
			return true
		case <-time.After(d):
			return false
		}
	}

	var clients []*ssh.Client
	for i := 0; i < 2; i++ {
		c, err := connect("user.12345678")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = c.Close() })
		clients = append(clients, c)
	}
	if c, err := connect("user.12345678"); err == nil {
		t.Cleanup(func() { _ = c.Close() })
		if !closedWithin(c, time.Second*3) {
			t.Fatal("expected a third connection for the same user to be closed")
		}
	}
	if closedWithin(clients[0], time.Millisecond*200) {
		t.Fatal("expected the connections within the limit to stay open")
	}

	// Another user has their own limit.
	other, err := connect("other.12345678")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = other.Close() })
	if closedWithin(other, time.Millisecond*500) {
		t.Fatal("expected a connection for another user to stay open")
	}
}
