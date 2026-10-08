package router

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gbrlsnchs/jwt/v3"
	ws "github.com/gorilla/websocket"

	"github.com/pterodactyl/wings/config"
	"github.com/pterodactyl/wings/environment"
	"github.com/pterodactyl/wings/remote"
	"github.com/pterodactyl/wings/router/tokens"
	"github.com/pterodactyl/wings/router/websocket"
	"github.com/pterodactyl/wings/server"
)

const websocketTestServerUuid = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"

type runningEnvironment struct {
	environment.ProcessEnvironment
}

func (runningEnvironment) State() string {
	return environment.ProcessRunningState
}

func startWebsocketTestServer(t *testing.T) string {
	t.Helper()
	url, _ := startWebsocketTestServerFor(t)
	return url
}

func startWebsocketTestServerFor(t *testing.T) (string, *server.Server) {
	t.Helper()
	cfg, err := config.NewAtPath(filepath.Join(t.TempDir(), "config.yml"))
	if err != nil {
		t.Fatal(err)
	}
	cfg.AuthenticationToken = "test-token"
	cfg.PanelLocation = "http://panel.test"
	config.Set(cfg)

	s, err := server.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	settings, _ := json.Marshal(map[string]string{"uuid": websocketTestServerUuid})
	if err := s.SyncWithConfiguration(remote.ServerConfigurationResponse{Settings: settings, ProcessConfiguration: &remote.ProcessConfiguration{}}); err != nil {
		t.Fatal(err)
	}
	s.Environment = runningEnvironment{}
	manager := server.NewEmptyManager(nil)
	manager.Add(s)

	srv := httptest.NewServer(Configure(manager, nil))
	t.Cleanup(srv.Close)
	return "ws" + strings.TrimPrefix(srv.URL, "http") + "/api/servers/" + websocketTestServerUuid + "/ws", s
}

func dialWebsocket(t *testing.T, url string) (*ws.Conn, error) {
	t.Helper()
	conn, _, err := ws.DefaultDialer.Dial(url, http.Header{"Origin": []string{"http://panel.test"}})
	if err == nil {
		t.Cleanup(func() { _ = conn.Close() })
	}
	return conn, err
}

// authenticationMessage returns a message authenticating with a valid token.
func authenticationMessage(t *testing.T) websocket.Message {
	t.Helper()
	// Tokens issued before Wings booted are rejected, and the issued time only has
	// second precision.
	now := time.Now().Add(time.Second * 2)
	payload := tokens.WebsocketPayload{
		Payload: jwt.Payload{
			IssuedAt:       jwt.NumericDate(now),
			ExpirationTime: jwt.NumericDate(now.Add(time.Minute * 10)),
		},
		Scoped:      tokens.Scoped{Scope: string(tokens.Websocket)},
		UserUUID:    "cccccccc-cccc-4ccc-8ccc-cccccccccccc",
		ServerUUID:  websocketTestServerUuid,
		Permissions: []string{websocket.PermissionConnect},
	}
	token, err := jwt.Sign(&payload, config.GetJwtAlgorithm())
	if err != nil {
		t.Fatal(err)
	}
	return websocket.Message{Event: websocket.AuthenticationEvent, Args: []string{string(token)}}
}

// authenticate sends a valid token over the socket and reports whether the
// server accepted it.
func authenticate(t *testing.T, conn *ws.Conn) bool {
	t.Helper()
	if err := conn.WriteJSON(authenticationMessage(t)); err != nil {
		return false
	}
	_ = conn.SetReadDeadline(time.Now().Add(time.Second * 5))
	defer func() { _ = conn.SetReadDeadline(time.Time{}) }()
	for {
		var m websocket.Message
		if err := conn.ReadJSON(&m); err != nil {
			return false
		}
		switch m.Event {
		case websocket.AuthenticationSuccessEvent:
			return true
		case websocket.ErrorEvent, websocket.JwtErrorEvent:
			return false
		}
	}
}

// Messages are handled in goroutines that can outlive the connection, so a client
// that authenticates and disconnects straight away must not leave its slot behind.
func TestWebsocketSlotsAreReleasedWhenClosedWhileAuthenticating(t *testing.T) {
	url, s := startWebsocketTestServerFor(t)

	msg := authenticationMessage(t)
	for i := 0; i < 200; i++ {
		conn, err := dialWebsocket(t, url)
		if err != nil {
			t.Fatalf("unexpected error opening connection %d: %v", i, err)
		}
		_ = conn.WriteJSON(msg)
		_ = conn.UnderlyingConn().Close()
	}

	deadline := time.Now().Add(time.Second * 5)
	for s.Websockets().Len() > 0 {
		if time.Now().After(deadline) {
			t.Fatalf("expected every slot to be released, %d are still held", s.Websockets().Len())
		}
		time.Sleep(time.Millisecond * 50)
	}

	conn, err := dialWebsocket(t, url)
	if err != nil {
		t.Fatalf("expected a user to be able to connect, got %v", err)
	}
	if !authenticate(t, conn) {
		t.Fatal("expected a user to be able to authenticate")
	}
}

func TestUnauthenticatedWebsocketsDoNotUseConnectionSlots(t *testing.T) {
	url := startWebsocketTestServer(t)

	// Connections that never authenticate.
	for i := 0; i < 30; i++ {
		if _, err := dialWebsocket(t, url); err != nil {
			t.Fatalf("unexpected error opening connection %d: %v", i, err)
		}
	}

	conn, err := dialWebsocket(t, url)
	if err != nil {
		t.Fatalf("expected a user to be able to connect, got %v", err)
	}
	if !authenticate(t, conn) {
		t.Fatal("expected a user to be able to authenticate")
	}
}

func TestUnauthenticatedWebsocketsAreClosed(t *testing.T) {
	previous := websocketAuthenticationTimeout
	websocketAuthenticationTimeout = time.Millisecond * 300
	t.Cleanup(func() { websocketAuthenticationTimeout = previous })
	url := startWebsocketTestServer(t)

	idle, err := dialWebsocket(t, url)
	if err != nil {
		t.Fatal(err)
	}
	authenticated, err := dialWebsocket(t, url)
	if err != nil {
		t.Fatal(err)
	}
	if !authenticate(t, authenticated) {
		t.Fatal("expected socket to authenticate")
	}

	_ = idle.SetReadDeadline(time.Now().Add(time.Second * 3))
	if _, _, err := idle.ReadMessage(); err == nil || strings.Contains(err.Error(), "i/o timeout") {
		t.Fatalf("expected the server to close an unauthenticated socket, got %v", err)
	}

	// The authenticated socket is not affected by the deadline.
	time.Sleep(websocketAuthenticationTimeout * 2)
	if !authenticate(t, authenticated) {
		t.Fatal("expected the authenticated socket to remain open")
	}
}

func TestAuthenticatedWebsocketsAreLimited(t *testing.T) {
	previous := maxWebsocketConnections
	maxWebsocketConnections = 2
	t.Cleanup(func() { maxWebsocketConnections = previous })
	url := startWebsocketTestServer(t)

	var conns []*ws.Conn
	for i := 0; i < 3; i++ {
		conn, err := dialWebsocket(t, url)
		if err != nil {
			t.Fatal(err)
		}
		conns = append(conns, conn)
	}
	if !authenticate(t, conns[0]) || !authenticate(t, conns[1]) {
		t.Fatal("expected sockets within the limit to authenticate")
	}
	if authenticate(t, conns[2]) {
		t.Fatal("expected socket over the limit to fail to authenticate")
	}
	if _, err := dialWebsocket(t, url); err == nil {
		t.Fatal("expected new connections to be refused once the limit is reached")
	}
}
