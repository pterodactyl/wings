package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/docker/docker/api/types/container"
	dockerclient "github.com/docker/docker/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pterodactyl/wings/config"
	"github.com/pterodactyl/wings/environment"
	"github.com/pterodactyl/wings/remote"
	"github.com/pterodactyl/wings/server/filesystem"
)

const installTestUUID = "5c7a1b9e-2f40-4d8a-9b3e-7a1c2d3e4f50"

// installerCreateRequest is the body of a container create call. The Config
// fields are sent at the top level and the HostConfig under its own key.
type installerCreateRequest struct {
	container.Config
	HostConfig *container.HostConfig `json:"HostConfig"`
}

// fakeInstallerDocker answers the Docker API calls made by the installer code
// path. It records the create body, fails the start call so Execute returns
// without a real container, and serves a fixed log body.
type fakeInstallerDocker struct {
	mu     sync.Mutex
	create *installerCreateRequest
	logs   string
}

func (f *fakeInstallerDocker) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()

	switch {
	case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/containers/create"):
		var req installerCreateRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		f.create = &req
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"Id":"installer-test-id","Warnings":[]}`))
	case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/start"):
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"message":"stopped by test after container create"}`))
	case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/logs"):
		_, _ = io.WriteString(w, f.logs)
	case r.Method == http.MethodDelete:
		w.WriteHeader(http.StatusNoContent)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

// newInstallerTestProcess builds an installation process whose Docker client
// talks to a fake API server, with a data directory, temp directory and log
// directory that are all private to the test.
func newInstallerTestProcess(t *testing.T, fake *fakeInstallerDocker) *InstallationProcess {
	t.Helper()

	srv := httptest.NewServer(fake)
	t.Cleanup(srv.Close)

	c, err := dockerclient.NewClientWithOpts(dockerclient.WithHost(strings.Replace(srv.URL, "http://", "tcp://", 1)))
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.Close() })

	cfg, err := config.NewAtPath(filepath.Join(t.TempDir(), "config.yml"))
	require.NoError(t, err)
	cfg.AuthenticationToken = "test-token"
	cfg.System.TmpDirectory = t.TempDir()
	cfg.System.LogDirectory = t.TempDir()
	config.Set(cfg)

	s, err := New(nil)
	require.NoError(t, err)
	s.cfg.Uuid = installTestUUID
	s.fs, err = filesystem.New(filepath.Join(t.TempDir(), installTestUUID), 0, nil)
	require.NoError(t, err)

	return &InstallationProcess{
		Server: s,
		Script: &remote.InstallationScript{
			ContainerImage: "ghcr.io/pterodactyl/installers:debian",
			Entrypoint:     "bash",
			Script:         "echo installing",
		},
		client: c,
	}
}

// captureInstallerCreate runs the installer until its container create call
// and returns the HostConfig and Config that were sent to Docker.
func captureInstallerCreate(t *testing.T) (*container.HostConfig, *container.Config) {
	t.Helper()

	fake := &fakeInstallerDocker{}
	p := newInstallerTestProcess(t, fake)

	_, err := p.Execute()
	require.Error(t, err, "the fake daemon fails the start call on purpose")

	fake.mu.Lock()
	defer fake.mu.Unlock()
	require.NotNil(t, fake.create, "installer never sent a container create request")
	require.NotNil(t, fake.create.HostConfig)
	return fake.create.HostConfig, &fake.create.Config
}

// The install container runs without the privileges installation scripts do
// not need.
func TestInstallContainerIsRestricted(t *testing.T) {
	hc, _ := captureInstallerCreate(t)

	// The Docker client sends capabilities by their full names.
	for _, capability := range []string{"CAP_NET_RAW", "CAP_MKNOD", "CAP_SETPCAP", "CAP_SYS_CHROOT"} {
		assert.Contains(t, hc.CapDrop, capability)
	}
	assert.Contains(t, hc.SecurityOpt, "no-new-privileges")

	require.NotNil(t, hc.Resources.PidsLimit)
	assert.GreaterOrEqual(t, *hc.Resources.PidsLimit, installerPidLimit)

	for _, m := range hc.Mounts {
		if m.Target == "/mnt/install" {
			assert.True(t, m.ReadOnly, "the installation script should be mounted read-only")
		}
	}
}

// The install log records each environment variable exactly as it was passed to
// the container.
func TestInstallLogKeepsEnvironmentValuesVerbatim(t *testing.T) {
	fake := &fakeInstallerDocker{logs: "installer output\n"}
	p := newInstallerTestProcess(t, fake)
	p.Server.cfg.EnvVars = environment.Variables{"DB_PASSWORD": "p&ss'word<1>"}

	require.NoError(t, os.MkdirAll(filepath.Join(config.Get().System.LogDirectory, "install"), 0o700))
	require.NoError(t, p.AfterExecute("installer-test-id"))

	body, err := os.ReadFile(p.GetLogPath())
	require.NoError(t, err)
	assert.Contains(t, string(body), "DB_PASSWORD=p&ss'word<1>")
}
