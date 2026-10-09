package sftp

import (
	"context"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"io"
	"net"
	"net/netip"
	"os"
	"path"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"emperror.dev/errors"
	"github.com/apex/log"
	"github.com/pkg/sftp"
	"golang.org/x/crypto/ed25519"
	"golang.org/x/crypto/ssh"

	"github.com/pterodactyl/wings/config"
	"github.com/pterodactyl/wings/internal/network"
	"github.com/pterodactyl/wings/remote"
	"github.com/pterodactyl/wings/server"
)

// Usernames all follow the same format, so don't even bother hitting the API if the username is not
// at least in the expected format. This is very basic protection against random bots finding the SFTP
// server and sending a flood of usernames.
var validUsernameRegexp = regexp.MustCompile(`^(?i)(.+)\.([a-z0-9]{8})$`)

// connectionLimits are the limits applied to connections.
type connectionLimits struct {
	// handshakeTimeout is the amount of time a connection has to complete the
	// handshake and authenticate.
	handshakeTimeout time.Duration
	// maxPending is the maximum number of connections that may be handshaking
	// at once.
	maxPending int
	// maxPendingPerIP is the maximum number of connections from a single IP
	// address, or IPv6 /64 network, that may be handshaking at once.
	maxPendingPerIP int
	// maxPerUser is the maximum number of authenticated connections a single
	// user may have open at once.
	maxPerUser int
}

// defaultLimits are the connection limits used by the SFTP server. The time
// allowed to authenticate matches the default LoginGraceTime of OpenSSH.
var defaultLimits = connectionLimits{
	handshakeTimeout: 2 * time.Minute,
	maxPending:       128,
	maxPendingPerIP:  16,
	maxPerUser:       32,
}

//goland:noinspection GoNameStartsWithPackageName
type SFTPServer struct {
	manager  *server.Manager
	BasePath string
	ReadOnly bool
	Listen   string
	// MaxConnections is the maximum number of connections that may be open at
	// once, or zero for no limit.
	MaxConnections int

	limits   connectionLimits
	pending  connectionCounter
	sessions connectionCounter
	// Dropped connections are logged separately for each reason, so that one
	// reason does not hide the other.
	droppedPending droppedLog
	droppedUser    droppedLog
}

// connectionCounter counts open connections in total and by a key, such as the
// address or the user they belong to.
type connectionCounter struct {
	mu    sync.Mutex
	total int
	byKey map[string]int
}

// acquire counts a new connection for the key, returning false if that would
// exceed either limit. A limit of zero is not enforced.
func (cc *connectionCounter) acquire(key string, maxTotal int, maxPerKey int) bool {
	cc.mu.Lock()
	defer cc.mu.Unlock()
	if cc.byKey == nil {
		cc.byKey = make(map[string]int)
	}
	if (maxTotal > 0 && cc.total >= maxTotal) || (maxPerKey > 0 && cc.byKey[key] >= maxPerKey) {
		return false
	}
	cc.total++
	cc.byKey[key]++
	return true
}

func (cc *connectionCounter) release(key string) {
	cc.mu.Lock()
	defer cc.mu.Unlock()
	cc.total--
	if cc.byKey[key] <= 1 {
		delete(cc.byKey, key)
	} else {
		cc.byKey[key]--
	}
}

// droppedLog limits how often dropped connections are logged.
type droppedLog struct {
	mu    sync.Mutex
	last  time.Time
	count int
}

func (d *droppedLog) log(message string, key string) {
	d.mu.Lock()
	d.count++
	if time.Since(d.last) < time.Second*10 {
		d.mu.Unlock()
		return
	}
	count := d.count
	d.count = 0
	d.last = time.Now()
	d.mu.Unlock()
	log.WithField("key", key).WithField("dropped", count).Warn("sftp: " + message)
}

// connectionKey returns the key used to limit connections from the address. All
// of the addresses in an IPv6 /64 network belong to the same user, so they share
// a key.
func connectionKey(addr net.Addr) string {
	host, _, err := net.SplitHostPort(addr.String())
	if err != nil {
		return addr.String()
	}
	ip, err := netip.ParseAddr(host)
	if err != nil {
		return host
	}
	ip = ip.Unmap().WithZone("")
	if ip.Is6() {
		if p, err := ip.Prefix(64); err == nil {
			return p.String()
		}
	}
	return ip.String()
}

func New(m *server.Manager) *SFTPServer {
	cfg := config.Get().System
	return &SFTPServer{
		manager:  m,
		BasePath: cfg.Data,
		ReadOnly: cfg.Sftp.ReadOnly,
		Listen:   cfg.Sftp.Address + ":" + strconv.Itoa(cfg.Sftp.Port),
		limits:   configuredLimits(cfg.Sftp),
	}
}

// configuredLimits returns the default connection limits with any overrides
// from the configuration applied.
func configuredLimits(cfg config.SftpConfiguration) connectionLimits {
	limits := defaultLimits
	if cfg.MaxConnectionsPerUser > 0 {
		limits.maxPerUser = cfg.MaxConnectionsPerUser
	}
	if cfg.MaxAuthenticatingConnectionsPerIP > 0 {
		limits.maxPendingPerIP = cfg.MaxAuthenticatingConnectionsPerIP
		// Every connection may come from the address of a proxy, so the limit for
		// all addresses must allow at least as many.
		limits.maxPending = max(limits.maxPending, limits.maxPendingPerIP)
	}
	return limits
}

// Run starts the SFTP server and add a persistent listener to handle inbound
// SFTP connections. This will automatically generate an ED25519 key if one does
// not already exist on the system for host key verification purposes.
func (c *SFTPServer) Run() error {
	if _, err := os.Stat(c.PrivateKeyPath()); os.IsNotExist(err) {
		if err := c.generateED25519PrivateKey(); err != nil {
			return err
		}
	} else if err != nil {
		return errors.Wrap(err, "sftp: could not stat private key file")
	}
	pb, err := os.ReadFile(c.PrivateKeyPath())
	if err != nil {
		return errors.Wrap(err, "sftp: could not read private key file")
	}
	private, err := ssh.ParsePrivateKey(pb)
	if err != nil {
		return err
	}

	conf := &ssh.ServerConfig{
		Config: ssh.Config{
			KeyExchanges: []string{
				"curve25519-sha256", "curve25519-sha256@libssh.org",
				"ecdh-sha2-nistp256", "ecdh-sha2-nistp384", "ecdh-sha2-nistp521",
				"diffie-hellman-group14-sha256",
			},
			Ciphers: []string{
				"aes128-gcm@openssh.com",
				"chacha20-poly1305@openssh.com",
				"aes128-ctr", "aes192-ctr", "aes256-ctr",
			},
			MACs: []string{
				"hmac-sha2-256-etm@openssh.com", "hmac-sha2-256",
			},
		},
		NoClientAuth: false,
		MaxAuthTries: 6,
		PasswordCallback: func(conn ssh.ConnMetadata, password []byte) (*ssh.Permissions, error) {
			return c.makeCredentialsRequest(conn, remote.SftpAuthPassword, string(password))
		},
		PublicKeyCallback: func(conn ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
			return c.makeCredentialsRequest(conn, remote.SftpAuthPublicKey, string(ssh.MarshalAuthorizedKey(key)))
		},
	}
	conf.AddHostKey(private)

	listener, err := net.Listen("tcp", c.Listen)
	if err != nil {
		return err
	}
	if c.MaxConnections > 0 {
		listener = network.LimitListener(listener, c.MaxConnections, "sftp")
	}

	public := string(ssh.MarshalAuthorizedKey(private.PublicKey()))
	log.WithField("listen", c.Listen).WithField("public_key", strings.Trim(public, "\n")).Info("sftp server listening for connections")

	return c.serve(listener, conf)
}

// serve accepts connections on the listener until it is closed.
func (c *SFTPServer) serve(listener net.Listener, conf *ssh.ServerConfig) error {
	var delay time.Duration
	for {
		conn, err := listener.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return err
			}
			// Accept fails immediately while the process is out of file descriptors,
			// so back off rather than spinning on it.
			if delay == 0 {
				delay = 5 * time.Millisecond
			} else {
				delay *= 2
			}
			if delay > time.Second {
				delay = time.Second
			}
			log.WithField("error", err).WithField("retry_in", delay).Error("sftp: failed to accept connection")
			time.Sleep(delay)
			continue
		}
		delay = 0

		key := connectionKey(conn.RemoteAddr())
		if !c.pending.acquire(key, c.limits.maxPending, c.limits.maxPendingPerIP) {
			c.droppedPending.log("too many unauthenticated connections, dropping connections", key)
			_ = conn.Close()
			continue
		}

		go func(conn net.Conn, key string) {
			defer conn.Close()
			sconn, chans, reqs, err := handshake(conn, conf, c.limits.handshakeTimeout)
			c.pending.release(key)
			if err != nil {
				log.WithField("error", err).WithField("ip", conn.RemoteAddr().String()).Error("sftp: failed to accept inbound connection")
				return
			}
			user := sessionKey(sconn)
			if !c.sessions.acquire(user, 0, c.limits.maxPerUser) {
				c.droppedUser.log("too many connections for user, dropping connections", user)
				_ = sconn.Close()
				return
			}
			defer c.sessions.release(user)
			if err := c.handle(sconn, chans, reqs); err != nil {
				log.WithField("error", err).WithField("ip", conn.RemoteAddr().String()).Error("sftp: failed to accept inbound connection")
			}
		}(conn, key)
	}
}

// sessionKey returns the key used to limit the connections of a user, which is
// the user and server the Panel authenticated.
func sessionKey(sconn *ssh.ServerConn) string {
	if sconn.Permissions != nil {
		if user := sconn.Permissions.Extensions["user"]; user != "" {
			return user + ":" + sconn.Permissions.Extensions["uuid"]
		}
	}
	return strings.ToLower(sconn.User())
}

// handshake performs the SSH handshake, including authentication, on the
// connection. The connection is closed if this does not complete in time.
func handshake(conn net.Conn, config *ssh.ServerConfig, timeout time.Duration) (*ssh.ServerConn, <-chan ssh.NewChannel, <-chan *ssh.Request, error) {
	if err := conn.SetDeadline(time.Now().Add(timeout)); err != nil {
		return nil, nil, nil, errors.WithStack(err)
	}
	sconn, chans, reqs, err := ssh.NewServerConn(conn, config)
	if err != nil {
		return nil, nil, nil, errors.WithStack(err)
	}
	if err := conn.SetDeadline(time.Time{}); err != nil {
		_ = sconn.Close()
		return nil, nil, nil, errors.WithStack(err)
	}
	return sconn, chans, reqs, nil
}

// AcceptInbound handles an inbound connection to the instance and determines if we should
// serve the request or not.
func (c *SFTPServer) AcceptInbound(conn net.Conn, config *ssh.ServerConfig) error {
	// Before beginning a handshake must be performed on the incoming net.Conn
	sconn, chans, reqs, err := handshake(conn, config, defaultLimits.handshakeTimeout)
	if err != nil {
		return err
	}
	return c.handle(sconn, chans, reqs)
}

// handle serves the channels of an authenticated connection.
func (c *SFTPServer) handle(sconn *ssh.ServerConn, chans <-chan ssh.NewChannel, reqs <-chan *ssh.Request) error {
	defer sconn.Close()
	go ssh.DiscardRequests(reqs)

	for ch := range chans {
		// If its not a session channel we just move on because its not something we
		// know how to handle at this point.
		if ch.ChannelType() != "session" {
			_ = ch.Reject(ssh.UnknownChannelType, "unknown channel type")
			continue
		}

		srv, ok := c.manager.Get(sconn.Permissions.Extensions["uuid"])
		if !ok {
			_ = ch.Reject(ssh.ConnectionFailed, "server not found")
			continue
		}

		channel, requests, err := ch.Accept()
		if err != nil {
			continue
		}

		go func(in <-chan *ssh.Request) {
			for req := range in {
				// Channels have a type that is dependent on the protocol. For SFTP
				// this is "subsystem" with a payload that (should) be "sftp". Discard
				// anything else we receive ("pty", "shell", etc)
				ok := req.Type == "subsystem" && len(req.Payload) >= 4 && string(req.Payload[4:]) == "sftp"
				_ = req.Reply(ok, nil)
			}
		}(requests)

		if err := c.Handle(sconn, srv, channel); err != nil {
			return err
		}
	}

	return nil
}

// Handle spins up a SFTP server instance for the authenticated user's server allowing
// them access to the underlying filesystem.
func (c *SFTPServer) Handle(conn *ssh.ServerConn, srv *server.Server, channel ssh.Channel) error {
	handler, err := NewHandler(conn, srv)
	if err != nil {
		return errors.WithStackIf(err)
	}

	ctx := srv.Sftp().Context(handler.User())
	rs := sftp.NewRequestServer(channel, handler.Handlers())

	// Close the session when access is revoked, and stop waiting for that once
	// the session ends on its own.
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			srv.Log().WithField("user", conn.User()).Warn("sftp: terminating active session")
			_ = rs.Close()
		case <-done:
		}
	}()

	if err := rs.Serve(); err == io.EOF {
		_ = rs.Close()
	}

	return nil
}

// Generates a new ED25519 private key that is used for host authentication when
// a user connects to the SFTP server.
func (c *SFTPServer) generateED25519PrivateKey() error {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return errors.Wrap(err, "sftp: failed to generate ED25519 private key")
	}
	if err := os.MkdirAll(path.Dir(c.PrivateKeyPath()), 0o700); err != nil {
		return errors.Wrap(err, "sftp: could not create internal sftp data directory")
	}
	o, err := os.OpenFile(c.PrivateKeyPath(), os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return errors.WithStack(err)
	}
	defer o.Close()

	b, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return errors.Wrap(err, "sftp: failed to marshal private key into bytes")
	}
	if err := pem.Encode(o, &pem.Block{Type: "PRIVATE KEY", Bytes: b}); err != nil {
		return errors.Wrap(err, "sftp: failed to write ED25519 private key to disk")
	}
	return nil
}

func (c *SFTPServer) makeCredentialsRequest(conn ssh.ConnMetadata, t remote.SftpAuthRequestType, p string) (*ssh.Permissions, error) {
	request := remote.SftpAuthRequest{
		Type:          t,
		User:          conn.User(),
		Pass:          p,
		IP:            conn.RemoteAddr().String(),
		SessionID:     conn.SessionID(),
		ClientVersion: conn.ClientVersion(),
	}

	logger := log.WithFields(log.Fields{"subsystem": "sftp", "method": request.Type, "username": request.User, "ip": request.IP})
	logger.Debug("validating credentials for SFTP connection")

	if !validUsernameRegexp.MatchString(request.User) {
		logger.Warn("failed to validate user credentials (invalid format)")
		return nil, &remote.SftpInvalidCredentialsError{}
	}

	resp, err := c.manager.Client().ValidateSftpCredentials(context.Background(), request)
	if err != nil {
		if _, ok := err.(*remote.SftpInvalidCredentialsError); ok {
			logger.Warn("failed to validate user credentials (invalid username or password)")
		} else {
			logger.WithField("error", err).Error("encountered an error while trying to validate user credentials")
		}
		return nil, err
	}

	logger.WithField("server", resp.Server).Debug("credentials validated and matched to server instance")
	permissions := ssh.Permissions{
		Extensions: map[string]string{
			"ip":          conn.RemoteAddr().String(),
			"uuid":        resp.Server,
			"user":        resp.User,
			"permissions": strings.Join(resp.Permissions, ","),
		},
	}

	return &permissions, nil
}

// PrivateKeyPath returns the path the host private key for this server instance.
func (c *SFTPServer) PrivateKeyPath() string {
	return path.Join(c.BasePath, ".sftp/id_ed25519")
}
