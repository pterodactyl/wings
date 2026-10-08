package websocket

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gbrlsnchs/jwt/v3"
	"github.com/gorilla/websocket"

	"github.com/pterodactyl/wings/config"
	"github.com/pterodactyl/wings/environment"
	"github.com/pterodactyl/wings/remote"
	"github.com/pterodactyl/wings/router/tokens"
	"github.com/pterodactyl/wings/server"
)

const (
	serverUuid      = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	otherServerUuid = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
)

var errCommandSent = errors.New("command sent")

// commandEnvironment is a running environment that records the commands sent
// to it.
type commandEnvironment struct {
	environment.ProcessEnvironment
	commands atomic.Int64
}

func (e *commandEnvironment) State() string {
	return environment.ProcessRunningState
}

func (e *commandEnvironment) SendCommand(string) error {
	e.commands.Add(1)
	// Returning an error skips saving the activity, which needs a database.
	return errCommandSent
}

func setupHandler(t *testing.T) (*Handler, *commandEnvironment) {
	t.Helper()

	cfg, err := config.NewAtPath(filepath.Join(t.TempDir(), "config.yml"))
	if err != nil {
		t.Fatal(err)
	}
	cfg.AuthenticationToken = "test-token"
	config.Set(cfg)

	s, err := server.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	settings, _ := json.Marshal(map[string]string{"uuid": serverUuid})
	if err := s.SyncWithConfiguration(remote.ServerConfigurationResponse{Settings: settings, ProcessConfiguration: &remote.ProcessConfiguration{}}); err != nil {
		t.Fatal(err)
	}
	env := &commandEnvironment{}
	s.Environment = env

	conns := make(chan *websocket.Conn, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		conns <- c
	}))
	t.Cleanup(srv.Close)

	client, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	go func() {
		for {
			if _, _, err := client.ReadMessage(); err != nil {
				return
			}
		}
	}()

	return &Handler{Connection: <-conns, server: s, limiter: NewLimiter()}, env
}

func signToken(t *testing.T, forServer string, permissions ...string) (string, *tokens.WebsocketPayload) {
	t.Helper()
	return signTokenFor(t, forServer, "cccccccc-cccc-4ccc-8ccc-cccccccccccc", permissions...)
}

func signTokenFor(t *testing.T, forServer string, userUuid string, permissions ...string) (string, *tokens.WebsocketPayload) {
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
		UserUUID:    userUuid,
		ServerUUID:  forServer,
		Permissions: permissions,
	}
	b, err := jwt.Sign(&payload, config.GetJwtAlgorithm())
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := NewTokenPayload(b)
	if err != nil {
		t.Fatal(err)
	}
	return string(b), parsed
}

// Activity is attributed to the user whose token authorized it, even if another
// user's token is on the socket since.
func TestActivityIsAttributedToTheAuthorizingToken(t *testing.T) {
	h, _ := setupHandler(t)
	_, authorizing := signTokenFor(t, serverUuid, "aaaaaaaa-0000-4000-8000-000000000001", PermissionConnect, PermissionSendCommand)
	_, other := signTokenFor(t, serverUuid, "bbbbbbbb-0000-4000-8000-000000000002", PermissionConnect)
	h.setJwt(other)

	a := h.activity(authorizing).Event(server.ActivityConsoleCommand, nil)
	if a.User.String != authorizing.UserUUID {
		t.Fatalf("expected activity to be attributed to %s, got %q", authorizing.UserUUID, a.User.String)
	}
}

func TestAuthenticationRejectsTokenForDifferentServer(t *testing.T) {
	h, _ := setupHandler(t)
	_, current := signToken(t, serverUuid, PermissionConnect)
	other, _ := signToken(t, otherServerUuid, PermissionConnect, PermissionSendCommand)
	h.setJwt(current)

	err := h.HandleInbound(context.Background(), Message{Event: AuthenticationEvent, Args: []string{other}})
	if !errors.Is(err, ErrJwtUuidMismatch) {
		t.Fatalf("expected a token for a different server to be rejected, got %v", err)
	}
	if h.GetJwt() != current {
		t.Fatal("expected the token on the socket to be unchanged")
	}
}

// Messages on a socket are handled concurrently, and commands are only ever
// authorized by a token for the server the socket belongs to.
func TestCommandIsNotAuthorizedByTokenForDifferentServer(t *testing.T) {
	if runtime.GOMAXPROCS(0) < 2 {
		t.Skip("the messages cannot be handled concurrently with a single CPU")
	}
	h, env := setupHandler(t)
	_, current := signToken(t, serverUuid, PermissionConnect)
	other, _ := signToken(t, otherServerUuid, PermissionConnect, PermissionSendCommand)

	deadline := time.Now().Add(time.Second * 5)
	for i := 0; i < 20000 && time.Now().Before(deadline); i++ {
		h.setJwt(current)
		h.limiter = NewLimiter()

		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = h.HandleInbound(context.Background(), Message{Event: AuthenticationEvent, Args: []string{other}})
		}()
		// Vary the order the two messages are handled in.
		for n := 0; n < i%64; n++ {
			_ = n * n
		}
		_ = h.HandleInbound(context.Background(), Message{Event: SendCommandEvent, Args: []string{"say hello"}})
		wg.Wait()

		if n := env.commands.Load(); n > 0 {
			t.Fatalf("expected the command to be refused (iteration %d, %d commands)", i, n)
		}
	}
}
