package sftp

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"io"
	"io/ioutil"
	"net"
	"os"
	"path"
	"regexp"
	"strconv"
	"strings"

	"emperror.dev/errors"
	"github.com/apex/log"
	"github.com/pkg/sftp"
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

// Starts the SFTP server and add a persistent listener to handle inbound SFTP connections.
func (c *SFTPServer) Run() error {
	keys, err := c.loadPrivateKeys()
	if err != nil {
		return err
	}

	conf := &ssh.ServerConfig{
		NoClientAuth:      false,
		MaxAuthTries:      6,
		PasswordCallback:  c.passwordCallback,
		PublicKeyCallback: c.publicKeyCallback,
	}

	for _, k := range keys {
		conf.AddHostKey(k)
	}

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
			_ = ch.Reject(ssh.UnknownChannelType, "unknown channel type")
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
				_ = req.Reply(req.Type == "subsystem" && string(req.Payload[4:]) == "sftp", nil)
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
			_ = conn.Close()
			continue
		}

		// Spin up a SFTP server instance for the authenticated user's server allowing
		// them access to the underlying filesystem.
		handler := sftp.NewRequestServer(channel, NewHandler(sconn, srv.Filesystem()).Handlers())
		if err := handler.Serve(); err == io.EOF {
			_ = handler.Close()
		}
	}
}

func (c *SFTPServer) loadPrivateKeys() ([]ssh.Signer, error) {
	if _, err := os.Stat(path.Join(c.BasePath, ".sftp/id_rsa")); err != nil {
		if !os.IsNotExist(err) {
			return nil, err
		}

		if err := c.generateRSAPrivateKey(); err != nil {
			return nil, err
		}
	}
	rsaBytes, err := ioutil.ReadFile(path.Join(c.BasePath, ".sftp/id_rsa"))
	if err != nil {
		return nil, errors.Wrap(err, "sftp/server: could not read private key file")
	}
	rsaPrivateKey, err := ssh.ParsePrivateKey(rsaBytes)
	if err != nil {
		return nil, err
	}

	if _, err := os.Stat(path.Join(c.BasePath, ".sftp/id_ecdsa")); err != nil {
		if !os.IsNotExist(err) {
			return nil, err
		}

		if err := c.generateECDSAPrivateKey(); err != nil {
			return nil, err
		}
	}
	ecdsaBytes, err := ioutil.ReadFile(path.Join(c.BasePath, ".sftp/id_ecdsa"))
	if err != nil {
		return nil, errors.Wrap(err, "sftp/server: could not read private key file")
	}
	ecdsaPrivateKey, err := ssh.ParsePrivateKey(ecdsaBytes)
	if err != nil {
		return nil, err
	}

	if _, err := os.Stat(path.Join(c.BasePath, ".sftp/id_ed25519")); err != nil {
		if !os.IsNotExist(err) {
			return nil, err
		}

		if err := c.generateEd25519PrivateKey(); err != nil {
			return nil, err
		}
	}
	ed25519Bytes, err := ioutil.ReadFile(path.Join(c.BasePath, ".sftp/id_ed25519"))
	if err != nil {
		return nil, errors.Wrap(err, "sftp/server: could not read private key file")
	}
	ed25519PrivateKey, err := ssh.ParsePrivateKey(ed25519Bytes)
	if err != nil {
		return nil, err
	}

	return []ssh.Signer{
		rsaPrivateKey,
		ecdsaPrivateKey,
		ed25519PrivateKey,
	}, nil
}

// generateRSAPrivateKey generates a RSA-4096 private key that will be used by the SFTP server.
func (c *SFTPServer) generateRSAPrivateKey() error {
	key, err := rsa.GenerateKey(rand.Reader, 4096)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(path.Join(c.BasePath, ".sftp"), 0755); err != nil {
		return errors.Wrap(err, "sftp/server: could not create .sftp directory")
	}
	o, err := os.OpenFile(path.Join(c.BasePath, ".sftp/id_rsa"), os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	defer o.Close()

	if err := pem.Encode(o, &pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	}); err != nil {
		return err
	}
	return nil
}

// generateECDSAPrivateKey generates a ECDSA-P256 private key that will be used by the SFTP server.
func (c *SFTPServer) generateECDSAPrivateKey() error {
	key, err := ecdsa.GenerateKey(elliptic.P521(), rand.Reader)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(path.Join(c.BasePath, ".sftp"), 0755); err != nil {
		return errors.Wrap(err, "sftp/server: could not create .sftp directory")
	}
	o, err := os.OpenFile(path.Join(c.BasePath, ".sftp/id_ecdsa"), os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	defer o.Close()

	privBytes, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return err
	}

	if err := pem.Encode(o, &pem.Block{
		Type:  "PRIVATE KEY",
		Bytes: privBytes,
	}); err != nil {
		return err
	}
	return nil
}

// generateEd25519PrivateKey generates a ed25519 private key that will be used by the SFTP server.
func (c *SFTPServer) generateEd25519PrivateKey() error {
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(path.Join(c.BasePath, ".sftp"), 0755); err != nil {
		return errors.Wrap(err, "sftp/server: could not create .sftp directory")
	}
	o, err := os.OpenFile(path.Join(c.BasePath, ".sftp/id_ed25519"), os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	defer o.Close()

	privBytes, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return err
	}

	if err := pem.Encode(o, &pem.Block{
		Type:  "PRIVATE KEY",
		Bytes: privBytes,
	}); err != nil {
		return err
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
		Type:          "password",
	}

	logger := log.WithFields(log.Fields{"subsystem": "sftp", "username": conn.User(), "ip": conn.RemoteAddr().String()})
	logger.Debug("validating credentials for SFTP connection")

	if !validUsernameRegexp.MatchString(request.User) {
		logger.Warn("failed to validate user credentials (invalid format)")
		return nil, &remote.SftpInvalidCredentialsError{}
	}

	if len(pass) < 1 {
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

func (c *SFTPServer) publicKeyCallback(conn ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
	request := remote.SftpAuthRequest{
		User:          conn.User(),
		Pass:          "KEKW",
		IP:            conn.RemoteAddr().String(),
		SessionID:     conn.SessionID(),
		ClientVersion: conn.ClientVersion(),
		Type:          "publicKey",
	}

	logger := log.WithFields(log.Fields{"subsystem": "sftp", "username": conn.User(), "ip": conn.RemoteAddr().String()})
	logger.Debug("validating public key for SFTP connection")

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

	if len(resp.SSHKeys) < 1 {
		return nil, &remote.SftpInvalidCredentialsError{}
	}

	for _, k := range resp.SSHKeys {
		storedPublicKey, _, _, _, err := ssh.ParseAuthorizedKey([]byte(k))
		if err != nil {
			return nil, err
		}

		if !bytes.Equal(key.Marshal(), storedPublicKey.Marshal()) {
			continue
		}

		return &ssh.Permissions{
			Extensions: map[string]string{
				"uuid":        resp.Server,
				"user":        conn.User(),
				"permissions": strings.Join(resp.Permissions, ","),
			},
		}, nil
	}
	return nil, &remote.SftpInvalidCredentialsError{}
}
