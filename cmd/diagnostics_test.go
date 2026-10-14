package cmd

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/pterodactyl/wings/config"
)

// The report describes the configuration file Wings was started with.
func TestDiagnosticsReadsConfigFromConfigPath(t *testing.T) {
	prevConfigPath := configPath
	prevArgs := diagnosticsArgs
	t.Cleanup(func() {
		configPath = prevConfigPath
		diagnosticsArgs = prevArgs
	})

	p := filepath.Join(t.TempDir(), "config.yml")
	cfg, err := config.NewAtPath(p)
	require.NoError(t, err)
	cfg.AuthenticationTokenId = "token-id"
	cfg.AuthenticationToken = "test-token"
	cfg.PanelLocation = "https://panel.example.test"
	require.NoError(t, config.WriteToDisk(cfg))

	configPath = p
	diagnosticsArgs.IncludeEndpoints = true

	var out strings.Builder
	got := writeConfiguration(&out)
	require.NotNil(t, got, out.String())
	require.Contains(t, out.String(), "https://panel.example.test")
}

// An upload that gets no response times out.
func TestDiagnosticsUploadTimesOut(t *testing.T) {
	prev := hastebinTimeout
	hastebinTimeout = 200 * time.Millisecond
	t.Cleanup(func() { hastebinTimeout = prev })

	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(release) })

	done := make(chan error, 1)
	go func() {
		_, err := uploadToHastebin(srv.URL, "report")
		done <- err
	}()

	select {
	case err := <-done:
		require.Error(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("expected the upload to time out")
	}
}

func TestDiagnosticsUploadReturnsDocumentURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/documents" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(`{"key":"abc123"}`))
	}))
	t.Cleanup(srv.Close)

	u, err := uploadToHastebin(srv.URL, "report")
	require.NoError(t, err)
	require.Equal(t, srv.URL+"/abc123", u)
}
