package downloader

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/pterodactyl/wings/config"
	"github.com/pterodactyl/wings/remote"
	"github.com/pterodactyl/wings/server"
)

const pullTestServerUuid = "ffffffff-ffff-4fff-8fff-ffffffffffff"

// newPullTestServer returns a server whose files are stored in a temporary data
// directory. Loopback is allowed so that the tests can use a local origin.
func newPullTestServer(t *testing.T) *server.Server {
	t.Helper()
	previous := config.Get()
	t.Cleanup(func() { config.Set(previous) })
	next := *previous
	next.System.Data = t.TempDir()
	next.Api.RemoteDownloadAllowlist = []string{"127.0.0.1"}
	config.Set(&next)

	settings, err := json.Marshal(map[string]string{"uuid": pullTestServerUuid})
	require.NoError(t, err)
	s, err := server.NewEmptyManager(nil).InitServer(pullTestServerUuid, remote.ServerConfigurationResponse{
		Settings:             settings,
		ProcessConfiguration: &remote.ProcessConfiguration{},
	})
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(s.Filesystem().Path(), 0o755))
	return s
}

// A file from a server that compresses responses for clients that accept gzip
// can be pulled.
func TestDownloadAcceptsGzipEncodedResponses(t *testing.T) {
	s := newPullTestServer(t)

	payload := []byte(strings.Repeat("pull test payload\n", 4096))
	var compressed bytes.Buffer
	zw := gzip.NewWriter(&compressed)
	_, err := zw.Write(payload)
	require.NoError(t, err)
	require.NoError(t, zw.Close())

	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := payload
		if strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
			w.Header().Set("Content-Encoding", "gzip")
			body = compressed.Bytes()
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		_, _ = w.Write(body)
	}))
	t.Cleanup(origin.Close)

	u, err := url.Parse(origin.URL + "/payload.bin")
	require.NoError(t, err)
	require.NoError(t, New(s, DownloadRequest{Directory: "/", URL: u, FileName: "payload.bin"}).Execute())

	got, err := os.ReadFile(filepath.Join(s.Filesystem().Path(), "payload.bin"))
	require.NoError(t, err)
	require.Equal(t, payload, got)
}

// Only the name of a file from Content-Disposition is used, so the file is saved
// in the directory that was asked for.
func TestDownloadFilenameFromHeaderIsSavedInRequestedDirectory(t *testing.T) {
	tests := []struct {
		directory string
		want      string
	}{
		{directory: "/chosen/dir", want: "chosen/dir/named.txt"},
		{directory: "chosen/dir", want: "chosen/dir/named.txt"},
		{directory: "", want: "named.txt"},
	}
	for _, tt := range tests {
		t.Run(tt.directory, func(t *testing.T) {
			s := newPullTestServer(t)

			origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Disposition", `attachment; filename="../../named.txt"`)
				w.Header().Set("Content-Length", "5")
				_, _ = w.Write([]byte("hello"))
			}))
			t.Cleanup(origin.Close)

			u, err := url.Parse(origin.URL + "/file")
			require.NoError(t, err)
			require.NoError(t, New(s, DownloadRequest{Directory: tt.directory, URL: u, FileName: "file", UseHeader: true}).Execute())

			root := s.Filesystem().Path()
			require.FileExists(t, filepath.Join(root, tt.want))
			require.NoFileExists(t, filepath.Join(filepath.Dir(root), "named.txt"))
		})
	}
}

// A download that fails to connect is reported as a failed download.
func TestDownloadConnectionFailureIsDownloadError(t *testing.T) {
	s := newPullTestServer(t)

	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := l.Addr().String()
	require.NoError(t, l.Close())

	u, err := url.Parse("http://" + addr + "/file")
	require.NoError(t, err)
	err = New(s, DownloadRequest{Directory: "/", URL: u, FileName: "file"}).Execute()
	require.Error(t, err)
	require.True(t, IsDownloadError(err), "got %v", err)
	require.ErrorIs(t, err, ErrDownloadFailed)
}
