package sftp

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"io"
	"io/ioutil"
	"net"
	"os"
	"path"
	"strconv"
	"strings"

	"emperror.dev/errors"
	"github.com/apex/log"
	"github.com/pkg/sftp"
	"github.com/pterodactyl/wings/api"
	"github.com/pterodactyl/wings/config"
	"github.com/pterodactyl/wings/server"
	"golang.org/x/crypto/ssh"
)

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

// Starts the SFTP server and add a persistent listener to handle inbound SFTP connections.
func (c *SFTPServer) Run() error {
	if _, err := os.Stat(path.Join(c.BasePath, ".sftp/id_rsa")); os.IsNotExist(err) {
		if err := c.generatePrivateKey(); err != nil {
			return err
		}
	} else if err != nil {
		return errors.Wrap(err, "sftp/server: could not stat private key file")
	}
	pb, err := ioutil.ReadFile(path.Join(c.BasePath, ".sftp/id_rsa"))
	if err != nil {
		return errors.Wrap(err, "sftp/server: could not read private key file")
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

	log.WithField("listen", c.Listen).Info("sftp server listening for connections")
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
			return s.Id() == uuid
		})
		if srv == nil {
			continue
		}

		// Spin up a SFTP server instance for the authenticated user's server allowing
		// them access to the underlying filesystem.
		handler := sftp.NewRequestServer(channel, NewHandler(sconn, srv.Filesystem()).Handlers())
		if err := handler.Serve(); err == io.EOF {
			handler.Close()
		}
	}
}

// Generates a private key that will be used by the SFTP server.
func (c *SFTPServer) generatePrivateKey() error {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return errors.WithStack(err)
	}
	if err := os.MkdirAll(path.Join(c.BasePath, ".sftp"), 0755); err != nil {
		return errors.Wrap(err, "sftp/server: could not create .sftp directory")
	}
	o, err := os.OpenFile(path.Join(c.BasePath, ".sftp/id_rsa"), os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return errors.WithStack(err)
	}
	defer o.Close()

	err = pem.Encode(o, &pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	})
	return errors.WithStack(err)
}

// A function capable of validating user credentials with the Panel API.
func (c *SFTPServer) passwordCallback(conn ssh.ConnMetadata, pass []byte) (*ssh.Permissions, error) {
	request := api.SftpAuthRequest{
		User:          conn.User(),
		Pass:          string(pass),
		IP:            conn.RemoteAddr().String(),
		SessionID:     conn.SessionID(),
		ClientVersion: conn.ClientVersion(),
	}

	logger := log.WithFields(log.Fields{"subsystem": "sftp", "username": conn.User(), "ip": conn.RemoteAddr().String()})
	logger.Debug("validating credentials for SFTP connection")

	resp, err := api.New().ValidateSftpCredentials(request)
	if err != nil {
		if api.IsInvalidCredentialsError(err) {
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
