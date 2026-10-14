package router

import (
	"bytes"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gbrlsnchs/jwt/v3"
	ws "github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"

	"github.com/pterodactyl/wings/config"
	"github.com/pterodactyl/wings/router/tokens"
	"github.com/pterodactyl/wings/router/websocket"
)

// authenticationMessageFor returns an authentication message carrying a token
// issued to the given user for the test server.
func authenticationMessageFor(t *testing.T, userUUID string) websocket.Message {
	t.Helper()
	now := time.Now().Add(time.Second * 2)
	payload := tokens.WebsocketPayload{
		Payload: jwt.Payload{
			IssuedAt:       jwt.NumericDate(now),
			ExpirationTime: jwt.NumericDate(now.Add(time.Minute * 10)),
		},
		Scoped:      tokens.Scoped{Scope: string(tokens.Websocket)},
		UserUUID:    userUUID,
		ServerUUID:  websocketTestServerUuid,
		Permissions: []string{websocket.PermissionConnect},
	}
	token, err := jwt.Sign(&payload, config.GetJwtAlgorithm())
	require.NoError(t, err)
	return websocket.Message{Event: websocket.AuthenticationEvent, Args: []string{string(token)}}
}

// authenticateAs sends a token for the given user and reports whether the
// server accepted it.
func authenticateAs(t *testing.T, conn *ws.Conn, userUUID string) bool {
	t.Helper()
	if err := conn.WriteJSON(authenticationMessageFor(t, userUUID)); err != nil {
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

// Revoking one user's access to a server closes that user's sockets and leaves
// other users' sockets on the server connected.
func TestDeauthorizeUserLeavesOtherUsersWebsocketsOpen(t *testing.T) {
	url := startWebsocketTestServer(t)
	const revokedUser = "eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee"
	const otherUser = "ffffffff-ffff-4fff-8fff-ffffffffffff"

	revokedConn, err := dialWebsocket(t, url)
	require.NoError(t, err)
	require.True(t, authenticateAs(t, revokedConn, revokedUser))

	otherConn, err := dialWebsocket(t, url)
	require.NoError(t, err)
	require.True(t, authenticateAs(t, otherConn, otherUser))

	base := url[:strings.Index(url, "/api/")]
	body, err := json.Marshal(map[string]any{"user": revokedUser, "servers": []string{websocketTestServerUuid}})
	require.NoError(t, err)
	req, err := http.NewRequest(http.MethodPost, "http"+strings.TrimPrefix(base, "ws")+"/api/deauthorize-user", bytes.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	_ = res.Body.Close()
	require.Equal(t, http.StatusNoContent, res.StatusCode)

	// The revoked user's socket is closed.
	require.NoError(t, revokedConn.SetReadDeadline(time.Now().Add(3*time.Second)))
	for {
		if _, _, err := revokedConn.ReadMessage(); err != nil {
			var ne net.Error
			require.False(t, errors.As(err, &ne) && ne.Timeout(), "expected the revoked user's socket to be closed")
			break
		}
	}

	// Sockets are closed in the background, so allow time for that.
	time.Sleep(200 * time.Millisecond)

	// The other user's socket is still open and can be used.
	require.True(t, authenticateAs(t, otherConn, otherUser), "revoking one user should not close another user's socket on the same server")
}
