package router

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gbrlsnchs/jwt/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pterodactyl/wings/config"
	"github.com/pterodactyl/wings/environment"
	"github.com/pterodactyl/wings/remote"
	"github.com/pterodactyl/wings/router/tokens"
	wserver "github.com/pterodactyl/wings/server"
	"github.com/pterodactyl/wings/server/transfer"
)

// transferTestUUID is a lowercase version 4 UUID, as the installer requires.
const transferTestUUID = "3f0c9a52-7d1e-4b8a-9c2f-5e6d7a8b9c01"

// transferTestPanel answers the Panel calls made by the transfer handlers. Only
// the calls these tests reach are implemented; any other call panics on the nil
// embedded interface.
type transferTestPanel struct {
	remote.Client

	mu        sync.Mutex
	config    remote.ServerConfigurationResponse
	statusErr error
	statuses  []bool
}

func (p *transferTestPanel) GetServerConfiguration(context.Context, string) (remote.ServerConfigurationResponse, error) {
	return p.config, nil
}

func (p *transferTestPanel) SetTransferStatus(_ context.Context, _ string, successful bool) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.statuses = append(p.statuses, successful)
	return p.statusErr
}

// transferTestEnvironment stands in for the Docker environment so that no
// container is needed.
type transferTestEnvironment struct {
	environment.ProcessEnvironment
}

func (transferTestEnvironment) Create() error { return nil }

func (transferTestEnvironment) State() string { return environment.ProcessOfflineState }

func (transferTestEnvironment) Destroy() error { return nil }

type transferHarness struct {
	t      *testing.T
	router http.Handler
	mgr    *wserver.Manager
	panel  *transferTestPanel
}

// newTransferHarness builds the full router with a temporary data directory and
// a node token of "test-token".
func newTransferHarness(t *testing.T, panel *transferTestPanel) *transferHarness {
	t.Helper()

	root := t.TempDir()
	cfg, err := config.NewAtPath(filepath.Join(root, "config.yml"))
	require.NoError(t, err)
	cfg.AuthenticationToken = "test-token"
	cfg.System.Data = filepath.Join(root, "volumes")
	cfg.System.MachineID.Enable = false
	config.Set(cfg)

	settings, err := json.Marshal(map[string]string{"uuid": transferTestUUID})
	require.NoError(t, err)
	panel.config = remote.ServerConfigurationResponse{
		Settings:             settings,
		ProcessConfiguration: &remote.ProcessConfiguration{},
	}

	mgr := wserver.NewEmptyManager(panel)
	return &transferHarness{t: t, router: Configure(mgr, panel), mgr: mgr, panel: panel}
}

// seedServer registers the test server on the manager, as an earlier receive
// would have, and replaces its Docker environment.
func (h *transferHarness) seedServer() *wserver.Server {
	h.t.Helper()

	s, err := h.mgr.InitServer(transferTestUUID, h.panel.config)
	require.NoError(h.t, err)
	s.Environment = transferTestEnvironment{}
	require.True(h.t, h.mgr.AddIfMissing(s))
	return s
}

// startInFlightReceive marks the server as receiving a transfer, the way the
// first POST /api/transfers request does.
func (h *transferHarness) startInFlightReceive(s *wserver.Server) *transfer.Transfer {
	h.t.Helper()

	s.SetTransferring(true)
	tr := transfer.New(context.Background(), s)
	require.True(h.t, transfer.Incoming().Add(tr))
	h.t.Cleanup(func() { transfer.Incoming().Remove(tr) })
	return tr
}

func (h *transferHarness) postTransfer(body *bytes.Buffer, contentType, bearer string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/api/transfers", body)
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Authorization", "Bearer "+bearer)
	w := httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	return w
}

func buildTransferTestArchive(t *testing.T, files map[string]string) []byte {
	t.Helper()

	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, body := range files {
		require.NoError(t, tw.WriteHeader(&tar.Header{
			Name:     name,
			Mode:     0o644,
			Size:     int64(len(body)),
			Typeflag: tar.TypeReg,
		}))
		_, err := tw.Write([]byte(body))
		require.NoError(t, err)
	}
	require.NoError(t, tw.Close())
	require.NoError(t, gz.Close())
	return buf.Bytes()
}

func transferTestMultipart(t *testing.T, archive []byte, checksum string) (*bytes.Buffer, string) {
	t.Helper()

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	part, err := mw.CreateFormFile("archive", "archive.tar.gz")
	require.NoError(t, err)
	_, err = part.Write(archive)
	require.NoError(t, err)
	require.NoError(t, mw.WriteField("checksum", checksum))
	require.NoError(t, mw.Close())
	return &buf, mw.FormDataContentType()
}

// signTransferTestToken issues a transfer JWT with the same claims the Panel
// sets: subject is the server UUID, scope is "transfer", and it expires after
// 15 minutes.
func signTransferTestToken(t *testing.T, subject string) string {
	t.Helper()

	token, err := jwt.Sign(tokens.TransferPayload{
		Payload: jwt.Payload{
			Subject:        subject,
			ExpirationTime: jwt.NumericDate(time.Now().Add(15 * time.Minute)),
		},
		Scoped: tokens.Scoped{Scope: string(tokens.ServerTransfer)},
	}, config.GetJwtAlgorithm())
	require.NoError(t, err)
	return string(token)
}

// A receive whose checksum does not match does not leave the extracted files on
// disk.
func TestTransferChecksumMismatchRemovesExtractedFiles(t *testing.T) {
	h := newTransferHarness(t, &transferTestPanel{})

	archive := buildTransferTestArchive(t, map[string]string{"received.txt": "data"})
	body, contentType := transferTestMultipart(t, archive, strings.Repeat("0", 64))

	w := h.postTransfer(body, contentType, signTransferTestToken(t, transferTestUUID))
	require.NotEqual(t, http.StatusOK, w.Code)

	_, err := os.Stat(filepath.Join(config.Get().System.Data, transferTestUUID, "received.txt"))
	assert.True(t, os.IsNotExist(err), "a file from a failed receive is still on disk")
	_, registered := h.mgr.Get(transferTestUUID)
	assert.False(t, registered)
}

// A second request to send a server that is already being received is refused,
// and does not affect the transfer in progress.
func TestTransferThatIsAlreadyInProgressIsRefused(t *testing.T) {
	panel := &transferTestPanel{}
	h := newTransferHarness(t, panel)
	s := h.seedServer()
	h.startInFlightReceive(s)

	archive := buildTransferTestArchive(t, map[string]string{"second.dat": "data"})
	body, contentType := transferTestMultipart(t, archive, strings.Repeat("0", 64))

	w := h.postTransfer(body, contentType, signTransferTestToken(t, transferTestUUID))
	require.Equal(t, http.StatusConflict, w.Code)

	_, registered := h.mgr.Get(transferTestUUID)
	assert.True(t, registered)
	assert.True(t, s.IsTransferring())
	assert.Empty(t, panel.statuses)
}

// A successful transfer is finished even if the Panel cannot be told about it,
// so that the server is not left unable to start.
func TestCompletedTransferIsFinishedWhenPanelFails(t *testing.T) {
	panel := &transferTestPanel{statusErr: errors.New("panel unavailable")}
	h := newTransferHarness(t, panel)
	s := h.seedServer()
	tr := h.startInFlightReceive(s)

	completeIncomingTransfer(h.mgr, tr, true)

	assert.False(t, s.IsTransferring())
	assert.Nil(t, transfer.Incoming().Get(transferTestUUID))
	assert.Equal(t, []bool{true}, panel.statuses)
}

// A failed transfer removes the server and the files that were received.
func TestFailedTransferRemovesReceivedServer(t *testing.T) {
	panel := &transferTestPanel{}
	h := newTransferHarness(t, panel)
	s := h.seedServer()
	tr := h.startInFlightReceive(s)
	require.NoError(t, s.EnsureDataDirectoryExists())
	require.NoError(t, os.WriteFile(filepath.Join(s.Filesystem().Path(), "received.txt"), []byte("data"), 0o644))

	completeIncomingTransfer(h.mgr, tr, false)

	_, registered := h.mgr.Get(transferTestUUID)
	assert.False(t, registered)
	_, err := os.Stat(s.Filesystem().Path())
	assert.True(t, os.IsNotExist(err))
	assert.Equal(t, []bool{false}, panel.statuses)
}

// An outbound transfer does not start while the server is being restored.
func TestPostServerTransferRejectsRestoringServer(t *testing.T) {
	h := newTransferHarness(t, &transferTestPanel{})
	s := h.seedServer()
	s.SetRestoring(true)

	payload := []byte(`{"url":"http://127.0.0.1:1/api/transfers","token":"Bearer test","server":{"uuid":"` +
		transferTestUUID + `","start_on_completion":false}}`)
	req := httptest.NewRequest(http.MethodPost, "/api/servers/"+transferTestUUID+"/transfer", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer test-token")
	w := httptest.NewRecorder()
	h.router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusConflict, w.Code)
	assert.False(t, s.IsTransferring())
}

// Cancelling an incoming transfer that does not exist is refused, rather than
// failing with a server error.
func TestDeleteTransferForUnknownServer(t *testing.T) {
	h := newTransferHarness(t, &transferTestPanel{})

	req := httptest.NewRequest(http.MethodDelete, "/api/transfers/"+transferTestUUID, nil)
	req.Header.Set("Authorization", "Bearer test-token")
	w := httptest.NewRecorder()
	h.router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusConflict, w.Code)
}
