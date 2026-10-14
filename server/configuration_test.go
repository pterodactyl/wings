package server

import (
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/pterodactyl/wings/remote"
)

func newConfiguredServer(t *testing.T) *Server {
	t.Helper()
	setTestConfig(t)

	s, err := New(nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SyncWithConfiguration(diskSpaceConfiguration(t, 1)); err != nil {
		t.Fatal(err)
	}
	return s
}

func diskSpaceConfiguration(t *testing.T, disk int64) remote.ServerConfigurationResponse {
	t.Helper()
	settings, err := json.Marshal(map[string]any{
		"uuid":  "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
		"build": map[string]any{"disk_space": disk},
	})
	if err != nil {
		t.Fatal(err)
	}
	return remote.ServerConfigurationResponse{
		Settings:             settings,
		ProcessConfiguration: &remote.ProcessConfiguration{},
	}
}

// Syncing replaces the configuration while other goroutines may be waiting to
// read it, and none of them should be left waiting forever.
func TestSyncWithConfigurationWhileReading(t *testing.T) {
	s := newConfiguredServer(t)
	responses := []remote.ServerConfigurationResponse{diskSpaceConfiguration(t, 1), diskSpaceConfiguration(t, 2)}

	done := make(chan struct{})
	go func() {
		defer close(done)
		var wg sync.WaitGroup
		for i := 0; i < 4; i++ {
			wg.Add(2)
			go func() {
				defer wg.Done()
				for j := 0; j < 500; j++ {
					if err := s.SyncWithConfiguration(responses[j%2]); err != nil {
						t.Error(err)
						return
					}
				}
			}()
			go func() {
				defer wg.Done()
				for j := 0; j < 500; j++ {
					_ = s.DiskSpace()
					_ = s.Config().GetUuid()
					_ = s.cfg.snapshot()
				}
			}()
		}
		wg.Wait()
	}()

	select {
	case <-done:
	case <-time.After(time.Second * 30):
		t.Fatal("timed out waiting for configuration readers and writers to finish")
	}
	if d := s.DiskSpace(); d != 1024*1024 && d != 2*1024*1024 {
		t.Fatalf("expected disk space from one of the synced configurations, got %d", d)
	}
}

// The configuration is returned by the API, so it must keep encoding its
// values at the top level of the object.
func TestConfigurationSnapshotJSON(t *testing.T) {
	s := newConfiguredServer(t)

	b, err := json.Marshal(s.cfg.snapshot())
	if err != nil {
		t.Fatal(err)
	}
	var v map[string]json.RawMessage
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatal(err)
	}
	if string(v["uuid"]) != `"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"` {
		t.Fatalf("expected uuid at the top level of the configuration, got %s", b)
	}
	if _, ok := v["build"]; !ok {
		t.Fatalf("expected build at the top level of the configuration, got %s", b)
	}
}
