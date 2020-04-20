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

// Performs a server backup and then emits the event over the server websocket. We
// let the actual backup system handle notifying the panel of the status, but that
// won't emit a websocket event.
func (s *Server) BackupLocal(b *backup.LocalBackup) error {
	// If no ignored files are present in the request, check for a .pteroignore file in the root
	// of the server files directory, and use that to generate the backup.
	if len(b.IgnoredFiles) == 0 {
		f, err := os.Open(path.Join(s.Filesystem.Path(), ".pteroignore"))
		if err != nil {
			if !os.IsNotExist(err) {
				zap.S().Warnw("failed to open .pteroignore file in server directory", zap.String("server", s.Uuid), zap.Error(errors.WithStack(err)))
			}
		} else {
			scanner := bufio.NewScanner(f)
			for scanner.Scan() {
				// Only include non-empty lines, for the sake of clarity...
				if t := scanner.Text(); t != "" {
					b.IgnoredFiles = append(b.IgnoredFiles, t)
				}
			}

			if err := scanner.Err(); err != nil {
				zap.S().Warnw("failed to scan .pteroignore file for lines", zap.String("server", s.Uuid), zap.Error(errors.WithStack(err)))
			}
		}
	}

	// Get the included files based on the root path and the ignored files provided.
	inc, err := s.Filesystem.GetIncludedFiles(s.Filesystem.Path(), b.IgnoredFiles)
	if err != nil {
		return errors.WithStack(err)
	}

	if err := b.Backup(inc, s.Filesystem.Path()); err != nil {
		if notifyError := s.notifyPanelOfBackup(b.Identifier(), &backup.ArchiveDetails{}, false); notifyError != nil {
			zap.S().Warnw("failed to notify panel of failed backup state", zap.String("backup", b.Uuid), zap.Error(err))
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
	s.Events().PublishJson(BackupCompletedEvent+":"+b.Uuid, map[string]interface{}{
		"uuid":        b.Uuid,
		"sha256_hash": ad.Checksum,
		"file_size":   ad.Size,
	})

	return nil
}
