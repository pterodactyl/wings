package server

import (
	"crypto/sha256"
	"encoding/hex"
	"github.com/mholt/archiver/v3"
	"github.com/pkg/errors"
	"github.com/pterodactyl/wings/api"
	"github.com/pterodactyl/wings/config"
	"go.uber.org/zap"
	"io"
	"os"
	"path"
	"strings"
	"sync"
)

type Backup struct {
	Uuid           string   `json:"uuid"`
	IgnoredFiles   []string `json:"ignored_files"`
	server         *Server
	localDirectory string
}

// Create a new Backup struct from data passed through in a request.
func (s *Server) NewBackup(uuid string, ignore []string) *Backup {
	return &Backup{
		Uuid:           uuid,
		IgnoredFiles:   ignore,
		server:         s,
		localDirectory: path.Join(config.Get().System.BackupDirectory, s.Uuid),
	}
}

// Locates the backup for a server and returns the local path. This will obviously only
// work if the backup was created as a local backup.
func (s *Server) LocateBackup(uuid string) (string, os.FileInfo, error) {
	p := path.Join(config.Get().System.BackupDirectory, s.Uuid, uuid+".tar.gz")

	st, err := os.Stat(p)
	if err != nil {
		return "", nil, err
	}

	if st.IsDir() {
		return "", nil, errors.New("invalid archive found; is directory")
	}

	return p, st, nil
}

// Ensures that the local backup destination for files exists.
func (b *Backup) ensureLocalBackupLocation() error {
	if _, err := os.Stat(b.localDirectory); err != nil {
		if !os.IsNotExist(err) {
			return errors.WithStack(err)
		}

		return os.MkdirAll(b.localDirectory, 0700)
	}

	return nil
}

// Returns the path for this specific backup.
func (b *Backup) GetPath() string {
	return path.Join(b.localDirectory, b.Uuid+".tar.gz")
}

func (b *Backup) GetChecksum() ([]byte, error) {
	h := sha256.New()

	f, err := os.Open(b.GetPath())
	if err != nil {
		return []byte{}, errors.WithStack(err)
	}
	defer f.Close()

	if _, err := io.Copy(h, f); err != nil {
		return []byte{}, errors.WithStack(err)
	}

	return h.Sum(nil), nil
}

// Generates a backup of the selected files and pushes it to the defined location
// for this instance.
func (b *Backup) Backup() (*api.BackupRequest, error) {
	rootPath := b.server.Filesystem.Path()

	if err := b.ensureLocalBackupLocation(); err != nil {
		return nil, errors.WithStack(err)
	}

	zap.S().Debugw("starting archive of server files for backup", zap.String("server", b.server.Uuid), zap.String("backup", b.Uuid))
	if err := archiver.Archive([]string{rootPath}, b.GetPath()); err != nil {
		if strings.HasPrefix(err.Error(), "file already exists") {
			zap.S().Debugw("backup already exists on system, removing and re-attempting", zap.String("backup", b.Uuid))

			if rerr := os.Remove(b.GetPath()); rerr != nil {
				return nil, errors.WithStack(rerr)
			}

			// Re-attempt this backup.
			return b.Backup()
		}

		// If there was some error with the archive, just go ahead and ensure the backup
		// is completely destroyed at this point. Ignore any errors from this function.
		os.Remove(b.GetPath())

		return nil, err
	}

	wg := sync.WaitGroup{}
	wg.Add(2)

	var checksum string
	// Calculate the checksum for the file.
	go func() {
		defer wg.Done()

		resp, err := b.GetChecksum()
		if err != nil {
			zap.S().Errorw("failed to calculate checksum for backup", zap.String("backup", b.Uuid), zap.Error(err))
		}

		checksum = hex.EncodeToString(resp)
	}()

	var s int64
	go func() {
		defer wg.Done()

		st, err := os.Stat(b.GetPath())
		if err != nil {
			return
		}

		s = st.Size()
	}()

	wg.Wait()

	return &api.BackupRequest{
		Successful: true,
		Sha256Hash: checksum,
		FileSize:   s,
	}, nil
}

// Performs a server backup and then notifies the Panel of the completed status
// so that the backup shows up for the user correctly.
func (b *Backup) BackupAndNotify() error {
	resp, err := b.Backup()
	if err != nil {
		b.notifyPanel(resp)

		return errors.WithStack(err)
	}

	if err := b.notifyPanel(resp); err != nil {
		// These errors indicate that the Panel will not know about the status of this
		// backup, so let's just go ahead and delete it, and let the Panel handle the
		// cleanup process for the backups.
		//
		// @todo perhaps in the future we can sync the backups from the servers on boot?
		os.Remove(b.GetPath())

		return err
	}

	// Emit an event over the socket so we can update the backup in realtime on
	// the frontend for the server.
	b.server.Events().PublishJson(BackupCompletedEvent+":"+b.Uuid, map[string]interface{}{
		"uuid":        b.Uuid,
		"sha256_hash": resp.Sha256Hash,
		"file_size":   resp.FileSize,
	})

	return nil
}

func (b *Backup) notifyPanel(request *api.BackupRequest) error {
	r := api.NewRequester()

	rerr, err := r.SendBackupStatus(b.server.Uuid, b.Uuid, *request)
	if rerr != nil || err != nil {
		if err != nil {
			zap.S().Errorw(
				"failed to notify panel of backup status due to internal code error",
				zap.String("server", b.server.Uuid),
				zap.String("backup", b.Uuid),
				zap.Error(err),
			)

			return err
		}

		zap.S().Warnw(
			rerr.String(),
			zap.String("server", b.server.Uuid),
			zap.String("backup", b.Uuid),
		)

		return errors.New(rerr.String())
	}

	return nil
}
