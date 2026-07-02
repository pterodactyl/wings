package router

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/apex/log"
	"github.com/gin-gonic/gin"

	"github.com/pterodactyl/wings/config"
	"github.com/pterodactyl/wings/environment"
	"github.com/pterodactyl/wings/events"
	"github.com/pterodactyl/wings/internal/models"
	"github.com/pterodactyl/wings/remote"
	wserver "github.com/pterodactyl/wings/server"
)

func init() {
	config.Set(&config.Configuration{AuthenticationToken: "test-token"})
}

type backupTestRemoteClient struct {
	restoreStatus chan string
}

func (c backupTestRemoteClient) GetBackupRemoteUploadURLs(context.Context, string, int64) (remote.BackupRemoteUploadResponse, error) {
	return remote.BackupRemoteUploadResponse{}, nil
}

func (c backupTestRemoteClient) GetInstallationScript(context.Context, string) (remote.InstallationScript, error) {
	return remote.InstallationScript{}, nil
}

func (c backupTestRemoteClient) GetServerConfiguration(context.Context, string) (remote.ServerConfigurationResponse, error) {
	return remote.ServerConfigurationResponse{}, nil
}

func (c backupTestRemoteClient) GetServers(context.Context, int) ([]remote.RawServerData, error) {
	return nil, nil
}

func (c backupTestRemoteClient) ResetServersState(context.Context) error {
	return nil
}

func (c backupTestRemoteClient) SetArchiveStatus(context.Context, string, bool) error {
	return nil
}

func (c backupTestRemoteClient) SetBackupStatus(context.Context, string, remote.BackupRequest) error {
	return nil
}

func (c backupTestRemoteClient) SendRestorationStatus(_ context.Context, backup string, _ bool) error {
	if c.restoreStatus != nil {
		select {
		case c.restoreStatus <- backup:
		default:
		}
	}
	return nil
}

func (c backupTestRemoteClient) SetInstallationStatus(context.Context, string, remote.InstallStatusRequest) error {
	return nil
}

func (c backupTestRemoteClient) SetTransferStatus(context.Context, string, bool) error {
	return nil
}

func (c backupTestRemoteClient) ValidateSftpCredentials(context.Context, remote.SftpAuthRequest) (remote.SftpAuthResponse, error) {
	return remote.SftpAuthResponse{}, nil
}

func (c backupTestRemoteClient) SendActivityLogs(context.Context, []models.Activity) error {
	return nil
}

type backupTestEnvironment struct{}

func (backupTestEnvironment) Type() string { return "test" }

func (backupTestEnvironment) Config() *environment.Configuration {
	return &environment.Configuration{}
}

func (backupTestEnvironment) Events() *events.Bus { return events.NewBus() }

func (backupTestEnvironment) Exists() (bool, error) { return true, nil }

func (backupTestEnvironment) IsRunning(context.Context) (bool, error) { return false, nil }

func (backupTestEnvironment) InSituUpdate() error { return nil }

func (backupTestEnvironment) OnBeforeStart(context.Context) error { return nil }

func (backupTestEnvironment) Start(context.Context) error { return nil }

func (backupTestEnvironment) Stop(context.Context) error { return nil }

func (backupTestEnvironment) WaitForStop(context.Context, time.Duration, bool) error {
	return nil
}

func (backupTestEnvironment) Terminate(context.Context, string) error { return nil }

func (backupTestEnvironment) Destroy() error { return nil }

func (backupTestEnvironment) ExitState() (uint32, bool, error) { return 0, false, nil }

func (backupTestEnvironment) Create() error { return nil }

func (backupTestEnvironment) Attach(context.Context) error { return nil }

func (backupTestEnvironment) SendCommand(string) error { return nil }

func (backupTestEnvironment) Readlog(int) ([]string, error) { return nil, nil }

func (backupTestEnvironment) State() string { return environment.ProcessOfflineState }

func (backupTestEnvironment) SetState(string) {}

func (backupTestEnvironment) Uptime(context.Context) (int64, error) { return 0, nil }

func (backupTestEnvironment) SetLogCallback(func([]byte)) {}

func newBackupRestoreContext(t *testing.T, client backupTestRemoteClient, backupID string, body string) (*gin.Context, *httptest.ResponseRecorder, *wserver.Server) {
	t.Helper()

	gin.SetMode(gin.TestMode)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/servers/server/backup/"+backupID+"/restore", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Params = gin.Params{
		{Key: "server", Value: "server"},
		{Key: "backup", Value: backupID},
	}

	s, err := wserver.New(client)
	if err != nil {
		t.Fatal(err)
	}
	s.Config().Uuid = "server"
	s.Environment = backupTestEnvironment{}

	c.Set("server", s)
	c.Set("api_client", client)
	c.Set("logger", log.WithField("test", t.Name()))

	return c, w, s
}

func TestPostServerRestoreBackupRejectsLoopbackDownloadURL(t *testing.T) {
	hit := make(chan struct{}, 1)
	internal := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit <- struct{}{}
		w.Header().Set("Content-Type", "application")
		_, _ = w.Write([]byte("not a gzip archive"))
	}))
	defer internal.Close()
	downloadURL := strings.Replace(internal.URL, "127.0.0.1", "localhost", 1)
	if downloadURL == internal.URL {
		t.Fatalf("expected test server URL to use 127.0.0.1, got %s", internal.URL)
	}

	client := backupTestRemoteClient{restoreStatus: make(chan string, 1)}
	backupID := "11111111-1111-1111-1111-111111111111"
	c, w, s := newBackupRestoreContext(t, client, backupID, fmt.Sprintf(`{"adapter":"s3","download_url":%q}`, downloadURL))
	defer s.CtxCancel()

	postServerRestoreBackup(c)

	if c.Writer.Status() != http.StatusBadRequest {
		t.Fatalf("expected restore request to be rejected, got status %d body %s", c.Writer.Status(), w.Body.String())
	}

	select {
	case <-hit:
		t.Fatal("expected loopback server not to receive restore download request")
	case <-time.After(100 * time.Millisecond):
	}
}

func TestPostServerRestoreBackupRejectsNonUuidBackupID(t *testing.T) {
	client := backupTestRemoteClient{restoreStatus: make(chan string, 1)}
	c, w, s := newBackupRestoreContext(t, client, "../target/archive", `{"adapter":"s3","download_url":"https://example.com/archive.tar.gz"}`)
	defer s.CtxCancel()

	postServerRestoreBackup(c)

	if c.Writer.Status() != http.StatusBadRequest {
		t.Fatalf("expected non-UUID backup id to be rejected, got status %d body %s", c.Writer.Status(), w.Body.String())
	}
}

func TestPostServerRestoreBackupRejectsBadDownloadStatus(t *testing.T) {
	setBackupRestoreAllowlist(t, []string{"127.0.0.1"})

	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-gzip")
		http.Error(w, "missing", http.StatusNotFound)
	}))
	defer remote.Close()

	client := backupTestRemoteClient{restoreStatus: make(chan string, 1)}
	backupID := "11111111-1111-1111-1111-111111111111"
	c, w, s := newBackupRestoreContext(t, client, backupID, fmt.Sprintf(`{"adapter":"s3","download_url":%q}`, remote.URL))
	defer s.CtxCancel()

	postServerRestoreBackup(c)

	if c.Writer.Status() != http.StatusBadRequest {
		t.Fatalf("expected restore request to be rejected, got status %d body %s", c.Writer.Status(), w.Body.String())
	}
}

func TestBackupRestoreContentTypeValidation(t *testing.T) {
	tests := map[string]bool{
		"application/gzip":                   true,
		"application/gzip; charset=binary":   true,
		"application/x-gzip":                 true,
		"application/x-gzip; charset=binary": true,
		"application":                        false,
		"gzip":                               false,
		"text/plain":                         false,
		"":                                   false,
	}

	for contentType, expected := range tests {
		if got := isSupportedBackupRestoreContentType(contentType); got != expected {
			t.Fatalf("expected content type %q support to be %v, got %v", contentType, expected, got)
		}
	}
}

func TestParseBackupUuid(t *testing.T) {
	tests := map[string]struct {
		expected string
		valid    bool
	}{
		"11111111-1111-1111-1111-111111111111": {expected: "11111111-1111-1111-1111-111111111111", valid: true},
		"11111111-1111-1111-1111-AAAAAAAAAAAA": {expected: "11111111-1111-1111-1111-aaaaaaaaaaaa", valid: true},
		"11111111111111111111111111111111":     {valid: false},
		"../target/archive":                    {valid: false},
	}

	for value, test := range tests {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		got, ok := parseBackupUuid(c, value)
		if ok != test.valid {
			t.Fatalf("expected validity for %q to be %v, got %v", value, test.valid, ok)
		}
		if got != test.expected {
			t.Fatalf("expected normalized backup UUID %q, got %q", test.expected, got)
		}
	}
}

func TestBackupRestoreBlockedIPValidation(t *testing.T) {
	setBackupRestoreAllowlist(t, nil)

	tests := map[string]bool{
		"127.0.0.1":     true,
		"10.0.0.1":      true,
		"169.254.1.1":   true,
		"100.64.0.1":    true,
		"198.18.0.1":    true,
		"::1":           true,
		"fe80::1":       true,
		"8.8.8.8":       false,
		"2606:4700::11": false,
	}

	for raw, expected := range tests {
		if got := isBlockedBackupRestoreIP("", net.ParseIP(raw)); got != expected {
			t.Fatalf("expected blocked state for %q to be %v, got %v", raw, expected, got)
		}
	}
}

func TestBackupRestoreDestinationAllowlist(t *testing.T) {
	setBackupRestoreAllowlist(t, []string{
		"minio.internal",
		"10.0.0.10",
		"192.168.50.0/24",
	})

	tests := []struct {
		name    string
		host    string
		ip      string
		blocked bool
	}{
		{name: "hostname", host: "minio.internal", ip: "10.0.0.20", blocked: false},
		{name: "ip", host: "10.0.0.10", ip: "10.0.0.10", blocked: false},
		{name: "cidr", host: "192.168.50.10", ip: "192.168.50.10", blocked: false},
		{name: "not listed", host: "10.0.0.11", ip: "10.0.0.11", blocked: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := isBlockedBackupRestoreIP(test.host, net.ParseIP(test.ip)); got != test.blocked {
				t.Fatalf("expected blocked state for %q/%q to be %v, got %v", test.host, test.ip, test.blocked, got)
			}
		})
	}
}

func TestBackupRestoreHTTPClientDoesNotLimitResponseBodyRead(t *testing.T) {
	client := backupRestoreHttpClient()
	if client.Timeout != 0 {
		t.Fatalf("expected restore client not to set total request timeout, got %s", client.Timeout)
	}
}

func setBackupRestoreAllowlist(t *testing.T, entries []string) {
	t.Helper()

	previous := config.Get()
	t.Cleanup(func() {
		config.Set(previous)
	})

	next := *previous
	next.System.Backups.RestoreHostAllowlist = entries
	config.Set(&next)
}
