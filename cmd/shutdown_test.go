package cmd

import (
	"net"
	"net/http"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/pterodactyl/wings/config"
	"github.com/pterodactyl/wings/server"
)

// On a signal the server states are saved and the HTTP server stops, so that
// Wings exits instead of being killed.
func TestShutdownOnSignalSavesStatesAndStopsServer(t *testing.T) {
	cfg, err := config.NewAtPath(filepath.Join(t.TempDir(), "config.yml"))
	require.NoError(t, err)
	cfg.AuthenticationToken = "test-token"
	cfg.System.RootDirectory = t.TempDir()
	config.Set(cfg)

	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	srv := &http.Server{ReadHeaderTimeout: time.Second}
	served := make(chan error, 1)
	go func() { served <- srv.Serve(l) }()

	signals := make(chan os.Signal, 1)
	go shutdownOnSignal(signals, server.NewEmptyManager(nil), srv)
	signals <- syscall.SIGTERM

	select {
	case err := <-served:
		require.ErrorIs(t, err, http.ErrServerClosed)
	case <-time.After(5 * time.Second):
		t.Fatal("expected the HTTP server to stop")
	}
	_, err = os.Stat(config.Get().System.GetStatesPath())
	require.NoError(t, err, "expected the server states to be saved")
}
