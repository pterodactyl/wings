package router

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/apex/log"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/pterodactyl/wings/config"
	"github.com/pterodactyl/wings/remote"
	wserver "github.com/pterodactyl/wings/server"
)

// A restore of a backup that cannot be found does not delete the server's
// files, even when truncate_directory is set.
func TestRestoreOfMissingBackupKeepsFiles(t *testing.T) {
	previous := config.Get()
	t.Cleanup(func() { config.Set(previous) })
	cfg, err := config.NewAtPath(filepath.Join(t.TempDir(), "config.yml"))
	require.NoError(t, err)
	cfg.AuthenticationToken = "test-token"
	cfg.System.Data = t.TempDir()
	cfg.System.BackupDirectory = t.TempDir()
	config.Set(cfg)

	const uuid = "dddddddd-dddd-4ddd-8ddd-dddddddddddd"
	settings, err := json.Marshal(map[string]any{"uuid": uuid})
	require.NoError(t, err)
	client := backupTestRemoteClient{}
	s, err := wserver.NewEmptyManager(client).InitServer(uuid, remote.ServerConfigurationResponse{Settings: settings, ProcessConfiguration: &remote.ProcessConfiguration{}})
	require.NoError(t, err)
	t.Cleanup(s.CtxCancel)
	s.Environment = backupTestEnvironment{}
	keep := filepath.Join(s.Filesystem().Path(), "keep.txt")
	require.NoError(t, os.WriteFile(keep, []byte("important"), 0o644))

	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	backupID := "6a3c2f6e-1b7d-4c8e-9f0a-2d4b6c8e0f12"
	c.Request = httptest.NewRequest(http.MethodPost, "/api/servers/"+uuid+"/backup/"+backupID+"/restore", strings.NewReader(`{"adapter":"wings","truncate_directory":true}`))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Params = gin.Params{{Key: "server", Value: uuid}, {Key: "backup", Value: backupID}}
	c.Set("server", s)
	c.Set("api_client", client)
	c.Set("logger", log.WithField("test", t.Name()))

	postServerRestoreBackup(c)

	require.NotEmpty(t, c.Errors, "expected the restore to fail")
	_, err = os.Stat(keep)
	require.NoError(t, err, "the server's files were deleted although the backup could not be found")
	require.False(t, s.IsRestoring())
}
