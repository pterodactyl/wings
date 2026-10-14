package cron

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pterodactyl/wings/config"
	"github.com/pterodactyl/wings/internal/database"
	"github.com/pterodactyl/wings/internal/models"
	"github.com/pterodactyl/wings/remote"
	"github.com/pterodactyl/wings/server"
	"github.com/pterodactyl/wings/system"
)

const (
	testServerUuid = "5b8ad3c2-6d1f-4e4a-9a53-0f4f2d7c1e10"
	testUserUuid   = "9d1e2c3b-4a5f-4b6c-8d7e-1f2a3b4c5d6e"
	validIP        = "203.0.113.9"
	// zoneIP is how an IPv6 address with a zone is recorded for an SFTP session.
	zoneIP = "[fe80::1%eth0]:2022"
)

var dbOnce sync.Once

// setupDatabase creates the local activity database once for this package and
// removes every stored row before a test runs.
func setupDatabase(t *testing.T) {
	t.Helper()
	dbOnce.Do(func() {
		dir, err := os.MkdirTemp("", "wings-cron-test-")
		if err != nil {
			panic(err)
		}
		config.Set(&config.Configuration{
			AuthenticationToken: "test-token",
			System:              config.SystemConfiguration{RootDirectory: dir},
		})
		if err := database.Initialize(); err != nil {
			panic(err)
		}
	})
	require.NoError(t, database.Instance().Where("1 = 1").Delete(&models.Activity{}).Error)
}

func insertActivity(t *testing.T, event models.Event, ip, file string) {
	t.Helper()
	meta := models.ActivityMeta{"files": []string{file}}
	if !strings.HasPrefix(string(event), "server:sftp.") {
		meta = models.ActivityMeta{"command": file}
	}
	a := (&models.Activity{
		Server:    testServerUuid,
		Event:     event,
		Metadata:  meta,
		IP:        ip,
		Timestamp: time.Now(),
	}).SetUser(testUserUuid)
	require.NoError(t, database.Instance().Create(a).Error)
}

// fakePanel stands in for POST /api/remote/activity. Like the Panel's ip rule
// (filter_var FILTER_VALIDATE_IP), it rejects the whole batch with a 422 when any
// row has an IP address that does not parse. It also rejects the command
// "refused".
type fakePanel struct {
	mu        sync.Mutex
	attempts  [][]string
	delivered []string
	sizes     []int
}

func (p *fakePanel) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	var body struct {
		Data []struct {
			IP       string         `json:"ip"`
			Metadata map[string]any `json:"metadata"`
		} `json:"data"`
	}
	_ = json.Unmarshal(raw, &body)

	ips := make([]string, 0, len(body.Data))
	valid := true
	for _, row := range body.Data {
		ips = append(ips, row.IP)
		if row.IP != "" && net.ParseIP(row.IP) == nil {
			valid = false
		}
		if row.Metadata["command"] == "refused" {
			valid = false
		}
	}

	p.mu.Lock()
	p.attempts = append(p.attempts, ips)
	p.sizes = append(p.sizes, len(raw))
	if valid {
		p.delivered = append(p.delivered, ips...)
	}
	p.mu.Unlock()

	if !valid {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"errors":[{"code":"ValidationException","status":"422","detail":"The data.0.ip field must be a valid IP address."}]}`))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (p *fakePanel) sentIPs() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []string
	for _, a := range p.attempts {
		out = append(out, a...)
	}
	return out
}

func (p *fakePanel) deliveredIPs() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.delivered...)
}

// newCrons returns both crons talking to the fake Panel through the real remote
// client.
func newCrons(t *testing.T, panel *fakePanel) (activityCron, sftpCron) {
	t.Helper()
	srv := httptest.NewServer(panel)
	t.Cleanup(srv.Close)
	manager := server.NewEmptyManager(remote.New(srv.URL))
	return activityCron{mu: system.NewAtomicBool(false), manager: manager, max: 100},
		sftpCron{mu: system.NewAtomicBool(false), manager: manager, max: 100}
}

// An SFTP event whose address carries an IPv6 zone is refused by the Panel, so it
// is dropped rather than sent.
func TestSftpActivityWithUnparsableIPIsNotSent(t *testing.T) {
	setupDatabase(t)
	panel := &fakePanel{}
	_, sc := newCrons(t, panel)
	insertActivity(t, "server:sftp.write", zoneIP, "/zone.txt")
	insertActivity(t, "server:sftp.write", validIP, "/valid.txt")

	require.NoError(t, sc.Run(context.Background()))

	assert.NotContains(t, panel.sentIPs(), "fe80::1%eth0")
	assert.Contains(t, panel.deliveredIPs(), validIP)
	assert.Zero(t, storedActivity(t))
}

// Activity the Panel refuses is dropped so that it does not stop the rest of the
// activity from being sent.
func TestRefusedActivityDoesNotHoldBackOtherActivity(t *testing.T) {
	setupDatabase(t)
	panel := &fakePanel{}
	ac, _ := newCrons(t, panel)
	for i := 0; i < 10; i++ {
		command := "say hi"
		if i == 4 {
			command = "refused"
		}
		insertActivity(t, "server:console.command", validIP, command)
	}

	require.NoError(t, ac.Run(context.Background()))

	assert.Len(t, panel.deliveredIPs(), 9)
	assert.Zero(t, storedActivity(t))
}

// The non-SFTP cron drops activity with an address that does not parse, and
// sends the rest.
func TestActivityWithUnparsableIPIsNotSent(t *testing.T) {
	setupDatabase(t)
	panel := &fakePanel{}
	ac, _ := newCrons(t, panel)
	insertActivity(t, "server:console.command", zoneIP, "say hi")
	insertActivity(t, "server:console.command", validIP, "say bye")

	require.NoError(t, ac.Run(context.Background()))

	assert.NotContains(t, panel.sentIPs(), "fe80::1%eth0")
	assert.Contains(t, panel.deliveredIPs(), validIP)
}

// All stored activity is sent in one run, rather than one batch per run.
func TestActivityBacklogIsSentInOneRun(t *testing.T) {
	setupDatabase(t)
	panel := &fakePanel{}
	ac, _ := newCrons(t, panel)
	insertMany(t, 350, "server:console.command", models.ActivityMeta{"command": "say hi"})

	require.NoError(t, ac.Run(context.Background()))

	assert.Len(t, panel.deliveredIPs(), 350)
	assert.Zero(t, storedActivity(t))
}

// SFTP events for one group are split over several events and requests.
func TestSftpEventsAreSentInBoundedRequests(t *testing.T) {
	setupDatabase(t)
	panel := &fakePanel{}
	_, sc := newCrons(t, panel)
	path := "/" + strings.Repeat("a", 4000)
	insertMany(t, 3000, "server:sftp.write", models.ActivityMeta{"files": []string{path}})

	require.NoError(t, sc.Run(context.Background()))

	assert.Zero(t, storedActivity(t))
	panel.mu.Lock()
	defer panel.mu.Unlock()
	require.NotEmpty(t, panel.sizes)
	for _, size := range panel.sizes {
		assert.LessOrEqual(t, size, maxRequestBytes+(64<<10))
	}
}

// Once more activity is stored than is allowed, the oldest is deleted.
func TestStoredActivityIsLimited(t *testing.T) {
	setupDatabase(t)
	insertMany(t, 50, "server:console.command", models.ActivityMeta{"command": "say hi"})

	require.NoError(t, pruneActivity(context.Background(), 20))

	assert.Equal(t, int64(20), storedActivity(t))
	var oldest models.Activity
	require.NoError(t, database.Instance().Order("id ASC").First(&oldest).Error)
	var newest models.Activity
	require.NoError(t, database.Instance().Order("id DESC").First(&newest).Error)
	assert.Equal(t, 19, newest.ID-oldest.ID)
}

func insertMany(t *testing.T, n int, event models.Event, meta models.ActivityMeta) {
	t.Helper()
	batch := make([]models.Activity, 0, n)
	for i := 0; i < n; i++ {
		batch = append(batch, models.Activity{
			Server:    testServerUuid,
			Event:     event,
			Metadata:  meta,
			IP:        validIP,
			Timestamp: time.Now(),
		})
	}
	require.NoError(t, database.Instance().CreateInBatches(&batch, 500).Error)
}

func storedActivity(t *testing.T) int64 {
	t.Helper()
	var count int64
	require.NoError(t, database.Instance().Model(&models.Activity{}).Count(&count).Error)
	return count
}
