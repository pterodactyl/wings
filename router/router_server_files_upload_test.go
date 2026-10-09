package router

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gbrlsnchs/jwt/v3"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/pterodactyl/wings/config"
	"github.com/pterodactyl/wings/internal/database"
	"github.com/pterodactyl/wings/remote"
	"github.com/pterodactyl/wings/router/tokens"
	"github.com/pterodactyl/wings/server"
)

const uploadTestServerUuid = "eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee"

// countingBody counts the bytes a handler reads from a request body.
type countingBody struct {
	io.ReadCloser
	read *atomic.Int64
}

func (b countingBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	b.read.Add(int64(n))
	return n, err
}

var (
	testDatabaseOnce sync.Once
	testDatabaseErr  error
)

// initTestDatabase opens the activity database, which can only be opened once
// per process.
func initTestDatabase(t *testing.T) {
	t.Helper()
	testDatabaseOnce.Do(func() {
		dir, err := os.MkdirTemp("", "wings-router-test")
		if err != nil {
			testDatabaseErr = err
			return
		}
		cfg := config.Get()
		cfg.System.RootDirectory = dir
		config.Set(cfg)
		testDatabaseErr = database.Initialize()
	})
	require.NoError(t, testDatabaseErr)
}

type uploadTest struct {
	url  string
	root string
	read *atomic.Int64
}

// newUploadTest starts the router with one server whose files live in a
// temporary directory. uploadLimit is in MiB and diskLimit in MiB (0 is
// unlimited).
func newUploadTest(t *testing.T, uploadLimit int64, diskLimit int64) *uploadTest {
	t.Helper()
	previous := config.Get()
	t.Cleanup(func() { config.Set(previous) })

	cfg, err := config.NewAtPath(filepath.Join(t.TempDir(), "config.yml"))
	require.NoError(t, err)
	cfg.AuthenticationToken = "test-token"
	cfg.PanelLocation = "http://panel.test"
	cfg.System.Data = t.TempDir()
	cfg.System.User.Uid = os.Getuid()
	cfg.System.User.Gid = os.Getgid()
	cfg.Api.UploadLimit = uploadLimit
	config.Set(cfg)
	initTestDatabase(t)

	settings, err := json.Marshal(map[string]any{
		"uuid":  uploadTestServerUuid,
		"build": map[string]any{"disk_space": diskLimit},
	})
	require.NoError(t, err)
	manager := server.NewEmptyManager(nil)
	s, err := manager.InitServer(uploadTestServerUuid, remote.ServerConfigurationResponse{Settings: settings, ProcessConfiguration: &remote.ProcessConfiguration{}})
	require.NoError(t, err)
	t.Cleanup(s.CtxCancel)
	manager.Add(s)

	read := &atomic.Int64{}
	engine := Configure(manager, nil)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Body = countingBody{ReadCloser: r.Body, read: read}
		engine.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)

	return &uploadTest{url: srv.URL, root: s.Filesystem().Path(), read: read}
}

func (u *uploadTest) token(t *testing.T) string {
	t.Helper()
	// Tokens issued before Wings boots are rejected, and the issued time only has
	// second precision.
	now := time.Now().Add(time.Second * 2)
	payload := tokens.UploadPayload{
		Payload: jwt.Payload{
			IssuedAt:       jwt.NumericDate(now),
			ExpirationTime: jwt.NumericDate(now.Add(time.Minute * 15)),
		},
		Scoped:     tokens.Scoped{Scope: string(tokens.FileUpload)},
		ServerUuid: uploadTestServerUuid,
		UserUuid:   "cccccccc-cccc-4ccc-8ccc-cccccccccccc",
		UniqueId:   uuid.NewString(),
	}
	token, err := jwt.Sign(&payload, config.GetJwtAlgorithm())
	require.NoError(t, err)
	return string(token)
}

// upload posts the files, keyed by name, and returns the response status and
// the size of the request body.
func (u *uploadTest) upload(t *testing.T, files map[string][]byte) (int, int64) {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	for name, content := range files {
		part, err := mw.CreateFormFile("files", name)
		require.NoError(t, err)
		_, err = part.Write(content)
		require.NoError(t, err)
	}
	require.NoError(t, mw.Close())
	size := int64(body.Len())

	req, err := http.NewRequest(http.MethodPost, u.url+"/upload/file?directory=/uploads&token="+u.token(t), &body)
	require.NoError(t, err)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		// The server may close the connection before reading all of the body.
		return 0, size
	}
	_ = res.Body.Close()
	return res.StatusCode, size
}

func TestUploadWritesFiles(t *testing.T) {
	u := newUploadTest(t, 1, 0)

	status, _ := u.upload(t, map[string][]byte{
		"a.txt": []byte("first"),
		"b.txt": []byte("second"),
	})
	require.Equal(t, http.StatusOK, status)

	for name, want := range map[string]string{"a.txt": "first", "b.txt": "second"} {
		got, err := os.ReadFile(filepath.Join(u.root, "uploads", name))
		require.NoError(t, err)
		require.Equal(t, want, string(got))
	}
}

// A file over the upload limit is refused once the limit is passed.
func TestUploadRejectsFileOverLimit(t *testing.T) {
	u := newUploadTest(t, 1, 0)

	status, size := u.upload(t, map[string][]byte{"big.bin": bytes.Repeat([]byte("a"), 40<<20)})
	if status != 0 {
		require.Equal(t, http.StatusBadRequest, status)
	}
	require.Less(t, u.read.Load(), size/2, "read %d of %d request body bytes before refusing the file", u.read.Load(), size)

	_, err := os.Stat(filepath.Join(u.root, "uploads", "big.bin"))
	require.ErrorIs(t, err, os.ErrNotExist)
}

// Uploads count against the server's disk limit while they are written.
func TestUploadRespectsDiskLimit(t *testing.T) {
	u := newUploadTest(t, 10, 1)

	status, _ := u.upload(t, map[string][]byte{"big.bin": bytes.Repeat([]byte("a"), 2<<20)})
	require.GreaterOrEqual(t, status, http.StatusBadRequest)

	_, err := os.Stat(filepath.Join(u.root, "uploads", "big.bin"))
	require.ErrorIs(t, err, os.ErrNotExist)
}
