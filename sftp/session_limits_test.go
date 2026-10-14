package sftp

import (
	"crypto/ed25519"
	"crypto/rand"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/apex/log"
	pkgsftp "github.com/pkg/sftp"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/ssh"

	"github.com/pterodactyl/wings/config"
	"github.com/pterodactyl/wings/server"
	"github.com/pterodactyl/wings/server/filesystem"
)

// setSessionTestConfig installs the minimum configuration the SFTP handlers read.
func setSessionTestConfig(t *testing.T) {
	t.Helper()

	config.Set(&config.Configuration{
		AuthenticationToken: "abc",
		System: config.SystemConfiguration{
			RootDirectory:     t.TempDir(),
			DiskCheckInterval: 150,
		},
	})
}

// startSessionHarness serves SSH connections through the SFTP server's own
// connection handler, so every channel goes through the real handle loop and
// the real Handle function. The server accepts any password.
func startSessionHarness(t *testing.T, c *SFTPServer, uuid string) string {
	t.Helper()

	_, key, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	signer, err := ssh.NewSignerFromKey(key)
	require.NoError(t, err)

	conf := &ssh.ServerConfig{
		PasswordCallback: func(ssh.ConnMetadata, []byte) (*ssh.Permissions, error) {
			return &ssh.Permissions{Extensions: map[string]string{
				"user":        "3f1c2a4e-5b6d-4e7f-8a9b-0c1d2e3f4a5b",
				"uuid":        uuid,
				"permissions": "*",
			}}, nil
		},
	}
	conf.AddHostKey(signer)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = listener.Close() })

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				sconn, chans, reqs, err := ssh.NewServerConn(conn, conf)
				if err != nil {
					_ = conn.Close()
					return
				}
				_ = c.handle(sconn, chans, reqs)
			}()
		}
	}()

	return listener.Addr().String()
}

// dialSessionHarness opens an authenticated SSH connection to the harness.
func dialSessionHarness(t *testing.T, addr string) *ssh.Client {
	t.Helper()

	client, err := ssh.Dial("tcp", addr, &ssh.ClientConfig{
		User:            "user.12345678",
		Auth:            []ssh.AuthMethod{ssh.Password("password")},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         5 * time.Second,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })

	return client
}

// settledGoroutines waits until the goroutine count stops changing and returns it.
func settledGoroutines() int {
	prev := -1
	for i := 0; i < 50; i++ {
		time.Sleep(20 * time.Millisecond)
		runtime.GC()
		n := runtime.NumGoroutine()
		if n == prev {
			return n
		}
		prev = n
	}
	return prev
}

// heapInUse returns the bytes of heap currently allocated.
func heapInUse() uint64 {
	runtime.GC()
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return m.HeapAlloc
}

// A session that has ended releases what it held, rather than waiting for the
// user's access to be revoked.
func TestSFTPSessionsReleaseTheirWatcherWhenClientCloses(t *testing.T) {
	setSessionTestConfig(t)

	srv, err := server.New(nil)
	require.NoError(t, err)
	t.Cleanup(func() { srv.Sftp().CancelAll() })

	manager := server.NewEmptyManager(nil)
	manager.Add(srv)
	c := &SFTPServer{manager: manager}
	client := dialSessionHarness(t, startSessionHarness(t, c, srv.ID()))

	openSession := func() {
		t.Helper()

		sess, err := client.NewSession()
		require.NoError(t, err)
		require.NoError(t, sess.RequestSubsystem("sftp"))
		require.NoError(t, sess.Close())
	}

	openSession()
	before := settledGoroutines()
	heapBefore := heapInUse()

	const sessions = 30
	for i := 0; i < sessions; i++ {
		openSession()
	}
	after := settledGoroutines()
	heapAfter := heapInUse()

	t.Logf("%d sessions ended: goroutines %d -> %d, heap in use %d -> %d bytes", sessions, before, after, heapBefore, heapAfter)
	require.LessOrEqual(t, after-before, 5, "a session that has ended should not leave a goroutine running")
}

// A channel for a server that is not registered on this node is refused.
func TestSFTPChannelsForMissingServerAreRefused(t *testing.T) {
	setSessionTestConfig(t)

	c := &SFTPServer{manager: server.NewEmptyManager(nil)}
	client := dialSessionHarness(t, startSessionHarness(t, c, "00000000-0000-0000-0000-000000000000"))

	before := settledGoroutines()
	accepted := 0
	for i := 0; i < 30; i++ {
		sess, err := client.NewSession()
		if err != nil {
			continue
		}
		accepted++
		t.Cleanup(func() { _ = sess.Close() })
	}
	after := settledGoroutines()

	t.Logf("%d channels accepted for a missing server, goroutines %d -> %d", accepted, before, after)
	require.Zero(t, accepted, "expected channels for a server that is not on this node to be refused")
}

// A session can only have a limited number of files open at once, and closing a
// file frees its slot.
func TestOpenedFileHandlesAreLimitedPerSession(t *testing.T) {
	setSessionTestConfig(t)

	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "file.txt"), []byte("data"), 0o644))
	fsys, err := filesystem.New(root, 0, nil)
	require.NoError(t, err)

	srv, err := server.New(nil)
	require.NoError(t, err)
	h := &Handler{
		server:      srv,
		fs:          fsys,
		permissions: []string{"*"},
		events:      &eventHandler{},
		logger:      log.WithField("subsystem", "sftp"),
	}

	var handles []io.Closer
	t.Cleanup(func() {
		for _, f := range handles {
			_ = f.Close()
		}
	})

	for i := 0; i < maxOpenFiles*2; i++ {
		f, err := h.Fileread(&pkgsftp.Request{Filepath: "file.txt"})
		if err != nil {
			break
		}
		handles = append(handles, f.(io.Closer))
	}
	require.Len(t, handles, maxOpenFiles)

	require.NoError(t, handles[0].Close())
	f, err := h.Fileread(&pkgsftp.Request{Filepath: "file.txt"})
	require.NoError(t, err, "closing a file should free a slot")
	handles[0] = f.(io.Closer)
}

// Connections for the same account share one limit.
func TestPerUserLimitUsesAuthenticatedAccount(t *testing.T) {
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
			Timeout:         5 * time.Second,
		})
	}
	closedWithin := func(client *ssh.Client, d time.Duration) bool {
		done := make(chan struct{})
		go func() {
			_ = client.Wait()
			close(done)
		}()
		select {
		case <-done:
			return true
		case <-time.After(d):
			return false
		}
	}

	for i := 0; i < 2; i++ {
		client, err := connect("user.12345678")
		require.NoError(t, err)
		t.Cleanup(func() { _ = client.Close() })
	}

	third, err := connect("USER.12345678")
	if err == nil {
		t.Cleanup(func() { _ = third.Close() })
		require.True(t, closedWithin(third, 3*time.Second), "a third connection for the same account should be closed")
	}
}
