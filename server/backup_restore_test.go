package server

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pterodactyl/wings/config"
	"github.com/pterodactyl/wings/environment"
	"github.com/pterodactyl/wings/remote"
	"github.com/pterodactyl/wings/server/backup"
	"github.com/pterodactyl/wings/server/filesystem"
)

const restoreTestUUID = "6a3c2f6e-1b7d-4c8e-9f0a-2d4b6c8e0f12"

// restoreTestClient only implements the Panel call that RestoreBackup makes.
type restoreTestClient struct {
	remote.Client
}

func (restoreTestClient) SendRestorationStatus(context.Context, string, bool) error {
	return nil
}

// restoreTestEnvironment reports the container as offline so RestoreBackup does
// not wait for a stop.
type restoreTestEnvironment struct {
	environment.ProcessEnvironment
}

func (restoreTestEnvironment) State() string { return environment.ProcessOfflineState }

// restoreTestBackup runs a hook before the real local restore, so a test can change
// server state while a restore is in progress.
type restoreTestBackup struct {
	*backup.LocalBackup
	beforeRestore func()
}

func (b restoreTestBackup) Restore(ctx context.Context, r io.Reader, cb backup.RestoreCallback) error {
	if b.beforeRestore != nil {
		b.beforeRestore()
	}
	return b.LocalBackup.Restore(ctx, r, cb)
}

// newRestoreTestServer returns a server with a real filesystem rooted in a temp
// directory, and a backup directory.
func newRestoreTestServer(t *testing.T) (*Server, string, string) {
	t.Helper()

	backupDir := t.TempDir()
	cfg := &config.Configuration{
		AuthenticationToken: "abc",
		System: config.SystemConfiguration{
			BackupDirectory:   backupDir,
			DiskCheckInterval: 150,
		},
	}
	cfg.System.User.Uid = os.Getuid()
	cfg.System.User.Gid = os.Getgid()
	config.Set(cfg)

	s, err := New(restoreTestClient{})
	require.NoError(t, err)
	s.Config().Uuid = "restore-test"
	s.Environment = restoreTestEnvironment{}

	root := filepath.Join(t.TempDir(), "server")
	require.NoError(t, os.Mkdir(root, 0o755))
	s.fs, err = filesystem.New(root, 0, nil)
	require.NoError(t, err)

	return s, backupDir, root
}

func writeRestoreTestArchive(t *testing.T, dir string, write func(tw *tar.Writer)) {
	t.Helper()

	f, err := os.Create(filepath.Join(dir, restoreTestUUID+".tar.gz"))
	require.NoError(t, err)
	defer f.Close()

	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	write(tw)
	require.NoError(t, tw.Close())
	require.NoError(t, gz.Close())
}

func addRestoreTestFile(t *testing.T, tw *tar.Writer, name string, mode int64, body string) {
	t.Helper()

	require.NoError(t, tw.WriteHeader(&tar.Header{
		Name:     name,
		Typeflag: tar.TypeReg,
		Mode:     mode,
		Size:     int64(len(body)),
		ModTime:  time.Unix(1700000000, 0),
	}))
	_, err := tw.Write([]byte(body))
	require.NoError(t, err)
}

func addRestoreTestHeader(t *testing.T, tw *tar.Writer, hdr *tar.Header) {
	t.Helper()

	hdr.ModTime = time.Unix(1700000000, 0)
	require.NoError(t, tw.WriteHeader(hdr))
}

func restoreTestLocal() backup.BackupInterface {
	return backup.NewLocal(restoreTestClient{}, restoreTestUUID, "")
}

// Entries are restored as the type they have in the archive.
func TestRestoreRecreatesEntryTypes(t *testing.T) {
	s, backupDir, root := newRestoreTestServer(t)
	writeRestoreTestArchive(t, backupDir, func(tw *tar.Writer) {
		addRestoreTestHeader(t, tw, &tar.Header{Name: "data", Typeflag: tar.TypeDir, Mode: 0o755})
		addRestoreTestFile(t, tw, "data/a.txt", 0o644, "hello")
		addRestoreTestHeader(t, tw, &tar.Header{Name: "data/link", Typeflag: tar.TypeSymlink, Linkname: "a.txt", Mode: 0o777})
		addRestoreTestHeader(t, tw, &tar.Header{Name: "device", Typeflag: tar.TypeChar, Mode: 0o644, Devmajor: 1, Devminor: 3})
	})

	require.NoError(t, s.RestoreBackup(restoreTestLocal(), nil, false))

	st, err := os.Lstat(filepath.Join(root, "data"))
	require.NoError(t, err)
	assert.True(t, st.IsDir())

	b, err := os.ReadFile(filepath.Join(root, "data", "a.txt"))
	require.NoError(t, err)
	assert.Equal(t, "hello", string(b))

	target, err := os.Readlink(filepath.Join(root, "data", "link"))
	require.NoError(t, err)
	assert.Equal(t, "a.txt", target)

	_, err = os.Lstat(filepath.Join(root, "device"))
	assert.ErrorIs(t, err, os.ErrNotExist, "special files are not restored")
}

// A restore does not change whether the server is suspended.
func TestRestoreDoesNotChangeSuspension(t *testing.T) {
	s, backupDir, _ := newRestoreTestServer(t)
	writeRestoreTestArchive(t, backupDir, func(tw *tar.Writer) {
		addRestoreTestFile(t, tw, "a.txt", 0o644, "hello")
	})

	// The server is suspended while the restore is running.
	b := restoreTestBackup{
		LocalBackup:   backup.NewLocal(restoreTestClient{}, restoreTestUUID, ""),
		beforeRestore: func() { s.Config().SetSuspended(true) },
	}
	require.NoError(t, s.RestoreBackup(b, nil, false))
	assert.True(t, s.IsSuspended())

	s.Config().SetSuspended(false)
	require.NoError(t, s.RestoreBackup(restoreTestLocal(), nil, false))
	assert.False(t, s.IsSuspended())
}

// With truncate set, the existing files are deleted before the backup is
// restored, and the server's directory itself is kept.
func TestRestoreTruncatesBeforeRestoring(t *testing.T) {
	s, backupDir, root := newRestoreTestServer(t)
	writeRestoreTestArchive(t, backupDir, func(tw *tar.Writer) {
		addRestoreTestFile(t, tw, "restored.txt", 0o644, "hello")
	})
	require.NoError(t, os.MkdirAll(filepath.Join(root, "old", "dir"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "old", "dir", "file.txt"), []byte("old"), 0o644))
	before, err := os.Stat(root)
	require.NoError(t, err)

	require.NoError(t, s.RestoreBackup(restoreTestLocal(), nil, true))

	_, err = os.Stat(filepath.Join(root, "old"))
	assert.ErrorIs(t, err, os.ErrNotExist)
	_, err = os.Stat(filepath.Join(root, "restored.txt"))
	assert.NoError(t, err)

	after, err := os.Stat(root)
	require.NoError(t, err)
	assert.True(t, os.SameFile(before, after), "the server directory should be kept")
}
