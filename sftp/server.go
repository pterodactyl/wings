package sftp

import (
	"context"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"io"
	"net"
	"os"
	"path"
	"regexp"
	"strconv"
	"strings"

	"emperror.dev/errors"
	"github.com/apex/log"
	"github.com/pkg/sftp"
	"golang.org/x/crypto/ed25519"
	"golang.org/x/crypto/ssh"

	"github.com/pterodactyl/wings/config"
	"github.com/pterodactyl/wings/remote"
	"github.com/pterodactyl/wings/server"
)

// Usernames all follow the same format, so don't even bother hitting the API if the username is not
// at least in the expected format. This is very basic protection against random bots finding the SFTP
// server and sending a flood of usernames.
var validUsernameRegexp = regexp.MustCompile(`^(?i)(.+)\.([a-z0-9]{8})$`)

//goland:noinspection GoNameStartsWithPackageName
type SFTPServer struct {
	manager  *server.Manager
	BasePath string
	ReadOnly bool
	Listen   string
}

func New(m *server.Manager) *SFTPServer {
	cfg := config.Get().System
	return &SFTPServer{
		manager:  m,
		BasePath: cfg.Data,
		ReadOnly: cfg.Sftp.ReadOnly,
		Listen:   cfg.Sftp.Address + ":" + strconv.Itoa(cfg.Sftp.Port),
	}
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
		NoClientAuth:     false,
		MaxAuthTries:     6,
		PasswordCallback: c.passwordCallback,
	}
	conf.AddHostKey(private)

	listener, err := net.Listen("tcp", c.Listen)
	if err != nil {
		return err
	}

	public := string(ssh.MarshalAuthorizedKey(private.PublicKey()))
	log.WithField("listen", c.Listen).WithField("public_key", strings.Trim(public, "\n")).Info("sftp server listening for connections")

	for {
		if conn, _ := listener.Accept(); conn != nil {
			go func(conn net.Conn) {
				defer conn.Close()
				c.AcceptInbound(conn, conf)
			}(conn)
		}
	}
}

// Handles an inbound connection to the instance and determines if we should serve the
// request or not.
func (c *SFTPServer) AcceptInbound(conn net.Conn, config *ssh.ServerConfig) {
	// Before beginning a handshake must be performed on the incoming net.Conn
	sconn, chans, reqs, err := ssh.NewServerConn(conn, config)
	if err != nil {
		return
	}
	defer sconn.Close()
	go ssh.DiscardRequests(reqs)

	for ch := range chans {
		// If its not a session channel we just move on because its not something we
		// know how to handle at this point.
		if ch.ChannelType() != "session" {
			ch.Reject(ssh.UnknownChannelType, "unknown channel type")
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
				req.Reply(req.Type == "subsystem" && string(req.Payload[4:]) == "sftp", nil)
			}
		}(requests)

		// If no UUID has been set on this inbound request then we can assume we
		// have screwed up something in the authentication code. This is a sanity
		// check, but should never be encountered (ideally...).
		//
		// This will also attempt to match a specific server out of the global server
		// store and return nil if there is no match.
		uuid := sconn.Permissions.Extensions["uuid"]
		srv := c.manager.Find(func(s *server.Server) bool {
			if uuid == "" {
				return false
			}
			return s.ID() == uuid
		})
		if srv == nil {
			continue
		}

		// Spin up a SFTP server instance for the authenticated user's server allowing
		// them access to the underlying filesystem.
		handler := sftp.NewRequestServer(channel, NewHandler(sconn, srv).Handlers())
		if err := handler.Serve(); err == io.EOF {
			handler.Close()
		}
	}
}

// Generates a new ED25519 private key that is used for host authentication when
// a user connects to the SFTP server.
func (c *SFTPServer) generateED25519PrivateKey() error {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return errors.Wrap(err, "sftp: failed to generate ED25519 private key")
	}
	if err := os.MkdirAll(path.Dir(c.PrivateKeyPath()), 0o755); err != nil {
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

// A function capable of validating user credentials with the Panel API.
func (c *SFTPServer) passwordCallback(conn ssh.ConnMetadata, pass []byte) (*ssh.Permissions, error) {
	request := remote.SftpAuthRequest{
		User:          conn.User(),
		Pass:          string(pass),
		IP:            conn.RemoteAddr().String(),
		SessionID:     conn.SessionID(),
		ClientVersion: conn.ClientVersion(),
	}

	logger := log.WithFields(log.Fields{"subsystem": "sftp", "username": conn.User(), "ip": conn.RemoteAddr().String()})
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
	sshPerm := &ssh.Permissions{
		Extensions: map[string]string{
			"uuid":        resp.Server,
			"user":        conn.User(),
			"permissions": strings.Join(resp.Permissions, ","),
		},
	}

	return sshPerm, nil
}

// PrivateKeyPath returns the path the host private key for this server instance.
func (c *SFTPServer) PrivateKeyPath() string {
	return path.Join(c.BasePath, ".sftp/id_ed25519")
}
