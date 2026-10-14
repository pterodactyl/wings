package router

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"gopkg.in/yaml.v2"

	"github.com/pterodactyl/wings/config"
	"github.com/pterodactyl/wings/remote"
	"github.com/pterodactyl/wings/server"
)

func TestPostUpdateConfigurationRotatesCredentials(t *testing.T) {
	t.Setenv("WINGS_TOKEN_ID", "")
	t.Setenv("WINGS_TOKEN", "")

	cfg, err := config.NewAtPath(filepath.Join(t.TempDir(), "config.yml"))
	if err != nil {
		t.Fatal(err)
	}
	cfg.AuthenticationTokenId = "old-id"
	cfg.AuthenticationToken = "old-token"
	if err := cfg.ResolveToken(false); err != nil {
		t.Fatal(err)
	}
	config.Set(cfg)

	credentials := make(chan [2]string, 1)
	manager := server.NewEmptyManager(backupTestRemoteClient{credentials: credentials})
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Set("manager", manager)
	c.Request = httptest.NewRequest("POST", "/api/update", strings.NewReader(`{"token_id":"new-id","token":"new-token"}`))
	c.Request.Header.Set("Content-Type", "application/json")

	postUpdateConfiguration(c)

	if recorder.Code != 200 {
		t.Fatalf("expected successful update, got status %d", recorder.Code)
	}
	updated := config.Get()
	if updated.Token.ID != "new-id" || updated.Token.Token != "new-token" {
		t.Fatalf("unexpected resolved credentials: %#v", updated.Token)
	}
	select {
	case got := <-credentials:
		if got != [2]string{"new-id", "new-token"} {
			t.Fatalf("unexpected client credentials: %#v", got)
		}
	default:
		t.Fatal("expected client credentials to be rotated")
	}
}

func TestPostCreateServerRejectsExistingServer(t *testing.T) {
	const uuid = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	s, err := server.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	settings, _ := json.Marshal(map[string]string{"uuid": uuid})
	if err := s.SyncWithConfiguration(remote.ServerConfigurationResponse{Settings: settings, ProcessConfiguration: &remote.ProcessConfiguration{}}); err != nil {
		t.Fatal(err)
	}
	manager := server.NewEmptyManager(backupTestRemoteClient{})
	manager.Add(s)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Set("manager", manager)
	c.Request = httptest.NewRequest("POST", "/api/servers", strings.NewReader(`{"uuid":"`+uuid+`"}`))
	c.Request.Header.Set("Content-Type", "application/json")

	postCreateServer(c)

	if recorder.Code != http.StatusConflict {
		t.Fatalf("expected a conflict, got status %d", recorder.Code)
	}
	if manager.Len() != 1 {
		t.Fatalf("expected one server, got %d", manager.Len())
	}
}

func TestPostUpdateConfigurationOnlyAppliesPanelValues(t *testing.T) {
	t.Setenv("WINGS_TOKEN_ID", "")
	t.Setenv("WINGS_TOKEN", "")

	p := filepath.Join(t.TempDir(), "config.yml")
	cfg, err := config.NewAtPath(p)
	if err != nil {
		t.Fatal(err)
	}
	cfg.AuthenticationTokenId = "old-id"
	cfg.AuthenticationToken = "old-token"
	cfg.System.Username = "pterodactyl"
	cfg.System.User.Uid = 988
	cfg.System.User.Gid = 988
	if err := cfg.ResolveToken(false); err != nil {
		t.Fatal(err)
	}
	config.Set(cfg)

	// Everything the Panel sends, plus values that only the node operator may set.
	body := `{
		"debug": true,
		"uuid": "node-uuid",
		"token_id": "new-id",
		"token": "new-token",
		"api": {
			"host": "127.0.0.1",
			"port": 8443,
			"ssl": {"enabled": true, "cert": "/srv/certs/node.pem", "key": "/srv/certs/node.key"},
			"upload_limit": 250,
			"trusted_proxies": ["10.0.0.0/8"]
		},
		"system": {
			"data": "/srv/data",
			"root_directory": "/srv/root",
			"sftp": {"bind_port": 2023, "bind_address": "127.0.0.2", "read_only": true},
			"user": {"uid": 1000, "gid": 1000, "rootless": {"enabled": true}},
			"username": "other",
			"passwd": {"enabled": true, "directory": "/srv/passwd"},
			"machineid": {"enabled": true, "directory": "/srv/machine-id"},
			"openatmode": "openat",
			"backups": {"restore_host_allowlist": ["10.0.0.0/8"]}
		},
		"docker": {
			"network": {"mode": "bridge"},
			"userns_mode": "private",
			"registries": {"registry.example.com": {"username": "u", "password": "p"}}
		},
		"allowed_mounts": ["/srv"],
		"allowed_origins": ["https://other.invalid"],
		"remote": "http://other.invalid",
		"ignore_panel_config_updates": false
	}`

	expected := config.Get()
	expected.Debug = true
	expected.Uuid = "node-uuid"
	expected.AuthenticationTokenId = "new-id"
	expected.AuthenticationToken = "new-token"
	expected.Api.Host = "127.0.0.1"
	expected.Api.Port = 8443
	expected.Api.Ssl.Enabled = true
	expected.Api.UploadLimit = 250
	expected.System.Sftp.Port = 2023
	want, err := yaml.Marshal(expected)
	if err != nil {
		t.Fatal(err)
	}

	manager := server.NewEmptyManager(backupTestRemoteClient{credentials: make(chan [2]string, 1)})
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Set("manager", manager)
	c.Request = httptest.NewRequest("POST", "/api/update", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	postUpdateConfiguration(c)

	if recorder.Code != 200 {
		t.Fatalf("expected successful update, got status %d", recorder.Code)
	}
	got, err := yaml.Marshal(config.Get())
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Fatalf("unexpected configuration after update:\n%s\nexpected:\n%s", got, want)
	}
	onDisk, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if string(onDisk) != string(want) {
		t.Fatalf("unexpected configuration written to disk:\n%s", onDisk)
	}
}
