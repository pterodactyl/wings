package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/apex/log"
	"github.com/apex/log/handlers/memory"

	"github.com/pterodactyl/wings/config"
	"github.com/pterodactyl/wings/events"
)

// setupMountTest creates a directory to hold mount sources and points the
// configuration at it. The permissions of the directories above it are outside
// the control of the test, so they are not checked.
func setupMountTest(t *testing.T, allowed ...string) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(root, 0o755); err != nil {
		t.Fatal(err)
	}
	previous := mountPathCheckRoot
	mountPathCheckRoot = root
	t.Cleanup(func() { mountPathCheckRoot = previous })

	cfg, err := config.NewAtPath(filepath.Join(t.TempDir(), "config.yml"))
	if err != nil {
		t.Fatal(err)
	}
	cfg.AuthenticationToken = "test-token"
	for _, a := range allowed {
		cfg.AllowedMounts = append(cfg.AllowedMounts, filepath.Join(root, a))
	}
	config.Set(cfg)
	return root
}

func mkdirs(t *testing.T, root string, dirs ...string) {
	t.Helper()
	for _, d := range dirs {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

func symlink(t *testing.T, target string, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
}

// mountSource returns the source of the custom mount for the given path, or an
// empty string if it was not mounted.
func mountSource(t *testing.T, source string) string {
	t.Helper()
	s, err := New(nil)
	if err != nil {
		t.Fatal(err)
	}
	s.cfg.Mounts = []Mount{{Source: source, Target: "/data"}}
	mounts := s.customMounts()
	if len(mounts) == 0 {
		return ""
	}
	return mounts[0].Source
}

func TestCustomMountsRequirePathContainment(t *testing.T) {
	root := setupMountTest(t, "mnt/")
	mkdirs(t, root, "mnt/data", "mnt2/data", "mntfoo", "outside")
	// A symlink inside of the allowed mount that points outside of it.
	symlink(t, filepath.Join(root, "outside"), filepath.Join(root, "mnt", "link"))

	tests := []struct {
		source  string
		allowed bool
	}{
		{source: "mnt", allowed: true},
		{source: "mnt/data", allowed: true},
		{source: "mnt/data/../data", allowed: true},
		{source: "mnt2/data", allowed: false},
		{source: "mntfoo", allowed: false},
		{source: "mnt/../outside", allowed: false},
		{source: "mnt/link", allowed: false},
		{source: "mnt/link/missing", allowed: false},
	}

	for _, tt := range tests {
		t.Run(tt.source, func(t *testing.T) {
			got := mountSource(t, filepath.Join(root, tt.source))
			if tt.allowed && got == "" {
				t.Fatalf("expected %s to be mounted", tt.source)
			}
			if !tt.allowed && got != "" {
				t.Fatalf("expected %s to be rejected, got %s", tt.source, got)
			}
		})
	}
}

// A source that does not exist is still passed along so that Docker refuses to
// start the server, rather than it starting without the mount.
func TestCustomMountsKeepMissingSources(t *testing.T) {
	root := setupMountTest(t, "mnt")
	mkdirs(t, root, "mnt")

	want := filepath.Join(root, "mnt", "missing", "data")
	if got := mountSource(t, want); got != want {
		t.Fatalf("expected missing source to be mounted as %s, got %q", want, got)
	}
}

// Sources beneath a directory that is writable by other users, or owned by the
// server user, are not mounted.
func TestCustomMountsRequireProtectedParentDirectories(t *testing.T) {
	for _, mode := range []os.FileMode{0o777, 0o775, 0o757} {
		t.Run(mode.String(), func(t *testing.T) {
			root := setupMountTest(t, "mnt")
			mkdirs(t, root, "mnt/shared/maps")
			if err := os.Chmod(filepath.Join(root, "mnt", "shared"), mode); err != nil {
				t.Fatal(err)
			}

			if got := mountSource(t, filepath.Join(root, "mnt", "shared", "maps")); got != "" {
				t.Fatalf("expected source beneath a writable directory to be rejected, got %s", got)
			}
			// The writable directory itself can still be mounted.
			if got := mountSource(t, filepath.Join(root, "mnt", "shared")); got == "" {
				t.Fatal("expected the writable directory itself to be mounted")
			}
		})
	}

	t.Run("owned by the server user", func(t *testing.T) {
		if os.Geteuid() != 0 {
			t.Skip("changing the owner of a directory requires root")
		}
		root := setupMountTest(t, "mnt")
		mkdirs(t, root, "mnt/shared/maps")
		if err := os.Chown(filepath.Join(root, "mnt", "shared"), 12345, 12345); err != nil {
			t.Fatal(err)
		}
		config.Update(func(c *config.Configuration) {
			c.System.User.Uid = 12345
			c.System.User.Gid = 12345
		})

		if got := mountSource(t, filepath.Join(root, "mnt", "shared", "maps")); got != "" {
			t.Fatalf("expected source beneath a directory owned by the server user to be rejected, got %s", got)
		}
	})
}

func TestCustomMountsResolveAllowedMounts(t *testing.T) {
	t.Run("symlinked allowed mount", func(t *testing.T) {
		root := setupMountTest(t, "data")
		mkdirs(t, root, "disk1/maps")
		symlink(t, filepath.Join(root, "disk1"), filepath.Join(root, "data"))

		want := filepath.Join(root, "disk1", "maps")
		if got := mountSource(t, filepath.Join(root, "data", "maps")); got != want {
			t.Fatalf("expected source to be mounted as %s, got %q", want, got)
		}
	})

	t.Run("allowed mount resolving to the root directory", func(t *testing.T) {
		root := setupMountTest(t, "shared")
		symlink(t, "/", filepath.Join(root, "shared"))

		if got := mountSource(t, "/usr"); got != "" {
			t.Fatalf("expected an allowed mount resolving to / to be ignored, got %s", got)
		}
	})

	t.Run("allowed mount beneath a writable directory", func(t *testing.T) {
		root := setupMountTest(t, "volumes/server/shared")
		mkdirs(t, root, "volumes/server", "other")
		symlink(t, filepath.Join(root, "other"), filepath.Join(root, "volumes", "server", "shared"))
		if err := os.Chmod(filepath.Join(root, "volumes", "server"), 0o777); err != nil {
			t.Fatal(err)
		}

		if got := mountSource(t, filepath.Join(root, "other")); got != "" {
			t.Fatalf("expected an allowed mount beneath a writable directory to be ignored, got %s", got)
		}
	})
}

// Resolving the allowed mounts touches the filesystem, which can block on a
// network share that is down, so servers without mounts should not do it.
func TestCustomMountsWithoutMountsDoNotResolveAllowedMounts(t *testing.T) {
	setupMountTest(t)
	config.Update(func(c *config.Configuration) {
		c.AllowedMounts = []string{"not/absolute"}
	})
	handler := memory.New()
	logger := log.Log.(*log.Logger)
	previous := logger.Handler
	logger.Handler = handler
	t.Cleanup(func() { logger.Handler = previous })

	s, err := New(nil)
	if err != nil {
		t.Fatal(err)
	}
	if mounts := s.customMounts(); len(mounts) != 0 {
		t.Fatalf("expected no custom mounts, got %v", mounts)
	}
	for _, e := range handler.Entries {
		if strings.Contains(e.Message, "allowed mount") {
			t.Fatalf("expected allowed mounts to not be resolved, got log entry %q", e.Message)
		}
	}
}

// A skipped mount is otherwise only visible in the Wings logs, while anything
// written to its target silently ends up inside the container.
func TestSkippedCustomMountIsReportedInConsole(t *testing.T) {
	root := setupMountTest(t, "mnt")
	mkdirs(t, root, "mnt", "outside")

	s, err := New(nil)
	if err != nil {
		t.Fatal(err)
	}
	ch := make(chan []byte, 8)
	s.Events().On(ch)
	s.cfg.Mounts = []Mount{{Source: filepath.Join(root, "outside"), Target: "/data"}}
	if mounts := s.customMounts(); len(mounts) != 0 {
		t.Fatalf("expected the mount to be skipped, got %v", mounts)
	}

	select {
	case b := <-ch:
		var e events.Event
		if err := events.DecodeTo(b, &e); err != nil {
			t.Fatal(err)
		}
		msg, _ := e.Data.(string)
		if e.Topic != ConsoleOutputEvent || !strings.Contains(msg, "/data") {
			t.Fatalf("expected a console message about the /data mount, got %s %q", e.Topic, msg)
		}
		if strings.Contains(msg, root) {
			t.Fatalf("expected the console message to not include host paths, got %q", msg)
		}
	case <-time.After(time.Second * 5):
		t.Fatal("expected a console message about the skipped mount")
	}
}
