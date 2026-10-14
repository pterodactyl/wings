package server

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/pterodactyl/wings/config"
	"github.com/pterodactyl/wings/environment"
)

// blockingStateEnvironment holds every call to State until it is released.
type blockingStateEnvironment struct {
	environment.ProcessEnvironment
	release chan struct{}
}

func (e *blockingStateEnvironment) State() string {
	<-e.release
	return environment.ProcessRunningState
}

// Each line of console output is checked before the next one is read.
func TestConsoleOutputIsCheckedAsItIsRead(t *testing.T) {
	cfg, err := config.NewAtPath(filepath.Join(t.TempDir(), "config.yml"))
	require.NoError(t, err)
	cfg.AuthenticationToken = "test-token"
	config.Set(cfg)

	s, err := New(nil)
	require.NoError(t, err)
	env := &blockingStateEnvironment{release: make(chan struct{})}
	s.Environment = env

	done := make(chan struct{})
	go func() {
		s.processConsoleOutputEvent([]byte("line"))
		close(done)
	}()

	select {
	case <-done:
		t.Fatal("expected the line to be checked before the next one is read")
	case <-time.After(100 * time.Millisecond):
	}

	close(env.release)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("expected the line to be processed once it could be checked")
	}
}
