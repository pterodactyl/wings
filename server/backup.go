package server

import (
	"bufio"
	"github.com/pkg/errors"
	"github.com/pterodactyl/wings/api"
	"github.com/pterodactyl/wings/server/backup"
	"go.uber.org/zap"
	"os"
	"path"
)

// Notifies the panel of a backup's state and returns an error if one is encountered
// while performing this action.
func (s *Server) notifyPanelOfBackup(uuid string, ad *backup.ArchiveDetails, successful bool) error {
	r := api.NewRequester()
	rerr, err := r.SendBackupStatus(uuid, ad.ToRequest(successful))
	if rerr != nil || err != nil {
		if err != nil {
			zap.S().Errorw(
				"failed to notify panel of backup status due to internal code error",
				zap.String("backup", s.Uuid),
				zap.Error(err),
			)

			return err
		}

		zap.S().Warnw(rerr.String(), zap.String("backup", uuid))

		return errors.New(rerr.String())
	}

	return nil
}

// Get all of the ignored files for a server based on its .pteroignore file in the root.
func (s *Server) getServerwideIgnoredFiles() ([]string, error) {
	var ignored []string

	f, err := os.Open(path.Join(s.Filesystem.Path(), ".pteroignore"))
	if err != nil {
		if !os.IsNotExist(err) {
			return nil, err
		}
	} else {
		scanner := bufio.NewScanner(f)
		for scanner.Scan() {
			// Only include non-empty lines, for the sake of clarity...
			if t := scanner.Text(); t != "" {
				ignored = append(ignored, t)
			}
		}

		if err := scanner.Err(); err != nil {
			return nil, err
		}
	}

	return ignored, nil
}

// Get the backup files to include when generating it.
func (s *Server) GetIncludedBackupFiles(ignored []string) (*backup.IncludedFiles, error) {
	// If no ignored files are present in the request, check for a .pteroignore file in the root
	// of the server files directory, and use that to generate the backup.
	if len(ignored) == 0 {
		if i, err := s.getServerwideIgnoredFiles(); err != nil {
			zap.S().Warnw("failed to retrieve server ignored files", zap.String("server", s.Uuid), zap.Error(err))
		} else {
			ignored = i
		}
	}

	// Get the included files based on the root path and the ignored files provided.
	return s.Filesystem.GetIncludedFiles(s.Filesystem.Path(), ignored)
}

// Performs a server backup and then emits the event over the server websocket. We
// let the actual backup system handle notifying the panel of the status, but that
// won't emit a websocket event.
func (s *Server) Backup(b backup.BackupInterface) error {
	// Get the included files based on the root path and the ignored files provided.
	inc, err := s.GetIncludedBackupFiles(b.Ignored())
	if err != nil {
		return errors.WithStack(err)
	}

	if err := b.Generate(inc, s.Filesystem.Path()); err != nil {
		if notifyError := s.notifyPanelOfBackup(b.Identifier(), &backup.ArchiveDetails{}, false); notifyError != nil {
			zap.S().Warnw("failed to notify panel of failed backup state", zap.String("backup", b.Identifier()), zap.Error(err))
		}

		return errors.WithStack(err)
	}

	// Try to notify the panel about the status of this backup. If for some reason this request
	// fails, delete the archive from the daemon and return that error up the chain to the caller.
	ad := b.Details()
	if notifyError := s.notifyPanelOfBackup(b.Identifier(), ad, true); notifyError != nil {
		b.Remove()

		return notifyError
	}

	// Emit an event over the socket so we can update the backup in realtime on
	// the frontend for the server.
	s.Events().PublishJson(BackupCompletedEvent+":"+b.Identifier(), map[string]interface{}{
		"uuid":        b.Identifier(),
		"sha256_hash": ad.Checksum,
		"file_size":   ad.Size,
	})

	return nil
}