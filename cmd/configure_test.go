package cmd

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/pterodactyl/wings/config"
)

// useConfigureArgs points the configure command at a fake Panel and the given
// --config-path and --config files, restoring the package state afterwards.
func useConfigureArgs(t *testing.T, panelURL, configurePath, globalPath string) {
	t.Helper()
	prevArgs := configureArgs
	prevConfigPath := configPath
	t.Cleanup(func() {
		configureArgs = prevArgs
		configPath = prevConfigPath
	})

	configureArgs.PanelURL = panelURL
	configureArgs.Token = "test-token"
	configureArgs.Node = "1"
	configureArgs.ConfigPath = configurePath
	configureArgs.Override = false
	configureArgs.AllowInsecure = false
	configPath = globalPath
}

// newFakePanel serves the node configuration endpoint with the given body.
func newFakePanel(t *testing.T, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/application/nodes/1/configuration" || r.Header.Get("Authorization") != "Bearer test-token" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func clearTokenEnvironment(t *testing.T) {
	t.Helper()
	t.Setenv("WINGS_TOKEN_ID", "")
	t.Setenv("WINGS_TOKEN", "")
}

func TestConfigureWritesToConfigPath(t *testing.T) {
	clearTokenEnvironment(t)
	dir := t.TempDir()
	configurePath := filepath.Join(dir, "configure.yml")
	globalPath := filepath.Join(dir, "global.yml")
	const existing = "uuid: existing-node\n"
	require.NoError(t, os.WriteFile(globalPath, []byte(existing), 0o600))

	panel := newFakePanel(t, `{"uuid":"11111111-2222-3333-4444-555555555555","token_id":"panel-id","token":"panel-token"}`)
	useConfigureArgs(t, panel.URL, configurePath, globalPath)

	configureCmdRun(nil, nil)

	got, err := os.ReadFile(globalPath)
	require.NoError(t, err)
	require.Equal(t, existing, string(got), "the --config file should not be written when --config-path is given")

	cfg, err := config.Load(configurePath)
	require.NoError(t, err)
	require.Equal(t, "panel-token", cfg.AuthenticationToken)
	require.Equal(t, panel.URL, cfg.PanelLocation)
}

// The token from the Panel is used as it is.
func TestConfigureRequiresTokenValue(t *testing.T) {
	clearTokenEnvironment(t)
	tokenFile := filepath.Join(t.TempDir(), "token")
	require.NoError(t, os.WriteFile(tokenFile, []byte("file-token"), 0o600))

	for _, token := range []string{"file://" + tokenFile, "$HOME"} {
		path := filepath.Join(t.TempDir(), "config.yml")
		body := `{"token_id":"panel-id","token":"` + token + `"}`

		require.Error(t, writePanelConfiguration(path, "http://panel.test", []byte(body)), token)
		require.NoFileExists(t, path)
	}
}

// Configuring an existing node keeps the settings the Panel does not manage.
func TestConfigureKeepsExistingSettings(t *testing.T) {
	clearTokenEnvironment(t)
	path := filepath.Join(t.TempDir(), "config.yml")
	require.NoError(t, os.WriteFile(path, []byte(`token_id: old-id
token: old-token
allowed_mounts:
  - /srv/shared
docker:
  registries:
    registry.example.com:
      username: user
      password: pass
`), 0o600))

	require.NoError(t, writePanelConfiguration(path, "http://panel.test", []byte(`{"token_id":"new-id","token":"new-token"}`)))

	cfg, err := config.Load(path)
	require.NoError(t, err)
	require.Equal(t, "new-id", cfg.AuthenticationTokenId)
	require.Equal(t, "new-token", cfg.AuthenticationToken)
	require.Equal(t, []string{"/srv/shared"}, cfg.AllowedMounts)
	require.Contains(t, cfg.Docker.Registries, "registry.example.com")
}
