package router

import (
	"bytes"
	"errors"
	"net"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/pterodactyl/wings/router/websocket"
)

// blockingConn is a connection whose writes can be made to block until it is
// closed.
type blockingConn struct {
	net.Conn
	blocked   atomic.Bool
	closeOnce sync.Once
	closed    chan struct{}
}

func (c *blockingConn) Write(p []byte) (int, error) {
	if c.blocked.Load() {
		<-c.closed
		return 0, net.ErrClosed
	}
	return c.Conn.Write(p)
}

func (c *blockingConn) Close() error {
	c.closeOnce.Do(func() { close(c.closed) })
	return c.Conn.Close()
}

type blockingListener struct {
	net.Listener
	accepted chan *blockingConn
}

func (l *blockingListener) Accept() (net.Conn, error) {
	conn, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	sc := &blockingConn{Conn: conn, closed: make(chan struct{})}
	l.accepted <- sc
	return sc, nil
}

// syncBuffer is a bytes.Buffer that can be written to from several goroutines.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// startBlockingWebsocketServer starts the websocket test server with a listener
// whose connections can block writes. Panics recovered by the router are written
// to the returned buffer.
func startBlockingWebsocketServer(t *testing.T) (string, *blockingListener, *syncBuffer) {
	t.Helper()
	recovered := &syncBuffer{}
	previous := gin.DefaultErrorWriter
	gin.DefaultErrorWriter = recovered
	t.Cleanup(func() { gin.DefaultErrorWriter = previous })

	manager, _ := newWebsocketTestManager(t)

	base, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	listener := &blockingListener{Listener: base, accepted: make(chan *blockingConn, 4)}
	srv := httptest.NewUnstartedServer(Configure(manager, nil))
	require.NoError(t, srv.Listener.Close())
	srv.Listener = listener
	srv.Start()
	t.Cleanup(srv.Close)

	return "ws" + strings.TrimPrefix(srv.URL, "http") + "/api/servers/" + websocketTestServerUuid + "/ws", listener, recovered
}

func isNetTimeout(err error) bool {
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}

// A socket that does not authenticate is closed once the authentication timeout
// passes.
func TestUnauthenticatedSocketIsClosedAfterTimeout(t *testing.T) {
	previous := websocketAuthenticationTimeout
	websocketAuthenticationTimeout = 300 * time.Millisecond
	t.Cleanup(func() { websocketAuthenticationTimeout = previous })

	url, listener, _ := startBlockingWebsocketServer(t)
	conn, err := dialWebsocket(t, url)
	require.NoError(t, err)
	serverSide := <-listener.accepted
	t.Cleanup(func() { _ = serverSide.Close() })

	// Ten messages pass the per-connection limiter. Each is answered with a JWT
	// error, and the per-event limiter adds one throttling notice.
	for i := 0; i < 10; i++ {
		require.NoError(t, conn.WriteJSON(websocket.Message{Event: websocket.SendStatsEvent}))
	}
	require.NoError(t, conn.SetReadDeadline(time.Now().Add(2*time.Second)))
	for i := 0; i < 5; i++ {
		var m websocket.Message
		require.NoError(t, conn.ReadJSON(&m), "reply %d", i)
	}

	// Block writes to the client, then send one more message, which is answered
	// with a throttling notice.
	serverSide.blocked.Store(true)
	require.NoError(t, conn.WriteJSON(websocket.Message{Event: websocket.SendStatsEvent}))

	require.NoError(t, conn.SetReadDeadline(time.Now().Add(3*time.Second)))
	_, _, err = conn.ReadMessage()
	require.Error(t, err)
	require.False(t, isNetTimeout(err), "expected the socket to be closed after the authentication timeout: %v", err)
}

// Only one write to a socket may be in progress at a time, including the
// throttling notice written by the read loop.
func TestWebsocketWritesAreSerialized(t *testing.T) {
	previous := websocketAuthenticationTimeout
	websocketAuthenticationTimeout = 500 * time.Millisecond
	t.Cleanup(func() { websocketAuthenticationTimeout = previous })

	url, listener, recovered := startBlockingWebsocketServer(t)
	conn, err := dialWebsocket(t, url)
	require.NoError(t, err)
	serverSide := <-listener.accepted
	t.Cleanup(func() { _ = serverSide.Close() })

	// Block the reply to the first message while it is being written.
	serverSide.blocked.Store(true)
	require.NoError(t, conn.WriteJSON(websocket.Message{Event: websocket.SendStatsEvent}))
	time.Sleep(100 * time.Millisecond)

	// Send more messages than the per-connection limiter allows while that write
	// is in progress.
	for i := 0; i < 12; i++ {
		require.NoError(t, conn.WriteJSON(websocket.Message{Event: websocket.SendStatsEvent}))
	}

	require.NoError(t, conn.SetReadDeadline(time.Now().Add(3*time.Second)))
	_, _, err = conn.ReadMessage()
	require.Error(t, err)
	// The connection can close before a recovered panic is logged.
	require.Never(t, func() bool {
		return strings.Contains(recovered.String(), "concurrent write")
	}, 500*time.Millisecond, 10*time.Millisecond, "expected writes to the socket not to overlap")
}
