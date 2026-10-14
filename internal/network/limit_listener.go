package network

import (
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/apex/log"
	"golang.org/x/net/netutil"
)

// limitWarnInterval is the minimum time between warnings that a listener has
// reached its limit.
const limitWarnInterval = time.Minute

// LimitListener returns a listener that accepts at most n connections at once,
// like netutil.LimitListener. Once the limit is reached, new connections wait
// for an open one to close without anything else being logged, so this logs a
// warning, at most once a minute, whenever the limit is reached.
func LimitListener(l net.Listener, n int, name string) net.Listener {
	return &limitListener{Listener: netutil.LimitListener(l, n), limit: int64(n), name: name}
}

type limitListener struct {
	net.Listener
	limit    int64
	name     string
	open     atomic.Int64
	lastWarn atomic.Int64
}

func (l *limitListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	if l.open.Add(1) >= l.limit {
		l.warn()
	}
	return &limitListenerConn{Conn: c, l: l}, nil
}

func (l *limitListener) warn() {
	now := time.Now().UnixNano()
	last := l.lastWarn.Load()
	if (last != 0 && now-last < int64(limitWarnInterval)) || !l.lastWarn.CompareAndSwap(last, now) {
		return
	}
	log.WithField("limit", l.limit).Warn(l.name + ": reached the maximum number of open connections, new connections will wait until others close (raise the open file limit for Wings to allow more)")
}

type limitListenerConn struct {
	net.Conn
	l    *limitListener
	once sync.Once
}

func (c *limitListenerConn) Close() error {
	err := c.Conn.Close()
	c.once.Do(func() { c.l.open.Add(-1) })
	return err
}
