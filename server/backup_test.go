package server

import (
	"context"
	"errors"
	"io"
	"path/filepath"
	"testing"

	"github.com/pterodactyl/wings/config"
	"github.com/pterodactyl/wings/internal/models"
	"github.com/pterodactyl/wings/remote"
	serverbackup "github.com/pterodactyl/wings/server/backup"
	serverfs "github.com/pterodactyl/wings/server/filesystem"
)

type backupTestRemoteClient struct {
	setBackupStatusErr error
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
	return c.setBackupStatusErr
}

func (c backupTestRemoteClient) SendRestorationStatus(context.Context, string, bool) error {
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

type successfulBackup struct {
	removed bool
}

func (b *successfulBackup) SetClient(remote.Client) {}

func (b *successfulBackup) Identifier() string {
	return "backup-id"
}

func (b *successfulBackup) WithLogContext(map[string]interface{}) {}

func (b *successfulBackup) Generate(context.Context, *serverfs.Filesystem, string) (*serverbackup.ArchiveDetails, error) {
	return &serverbackup.ArchiveDetails{
		Checksum:     "checksum",
		ChecksumType: "sha1",
		Size:         1024,
	}, nil
}

func (b *successfulBackup) Ignored() string {
	return "ignored"
}

func (b *successfulBackup) Checksum() ([]byte, error) {
	return nil, nil
}

func (b *successfulBackup) Size() (int64, error) {
	return 0, nil
}

func (b *successfulBackup) Path() string {
	return ""
}

func (b *successfulBackup) Details(context.Context, []remote.BackupPart) (*serverbackup.ArchiveDetails, error) {
	return nil, nil
}

func (b *successfulBackup) Remove() error {
	b.removed = true
	return nil
}

func (b *successfulBackup) Restore(context.Context, io.Reader, serverbackup.RestoreCallback) error {
	return nil
}

func TestServerBackupReturnsNotifyErrorAfterSuccessfulGeneration(t *testing.T) {
	config.Set(&config.Configuration{
		AuthenticationToken: "test-token",
		System: config.SystemConfiguration{
			DiskCheckInterval: 150,
		},
	})

	notifyErr := errors.New("panel unavailable")
	srv, err := New(backupTestRemoteClient{setBackupStatusErr: notifyErr})
	if err != nil {
		t.Fatal(err)
	}

	fsys, err := serverfs.New(filepath.Join(t.TempDir(), "server"), 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	srv.fs = fsys

	backup := &successfulBackup{}
	if err := srv.Backup(backup); !errors.Is(err, notifyErr) {
		t.Fatalf("expected notify error, got %v", err)
	}

	if !backup.removed {
		t.Fatal("expected backup archive to be removed after notify failure")
	}
}
