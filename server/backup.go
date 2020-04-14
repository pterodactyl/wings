package server

import (
	"github.com/pkg/errors"
	"github.com/pterodactyl/wings/server/backup"
	"go.uber.org/zap"
)

// Performs a server backup and then emits the event over the server websocket. We
// let the actual backup system handle notifying the panel of the status, but that
// won't emit a websocket event.
func (s *Server) BackupRoot(b *backup.Backup) error {
	r, err := b.LocalBackup(s.Filesystem.Path())
	if err != nil {
		if notifyError := b.NotifyPanel(r, false); notifyError != nil {
			zap.S().Warnw("failed to notify panel of failed backup state", zap.String("backup", b.Uuid), zap.Error(err))
		}

		return errors.WithStack(err)
	}

	// Try to notify the panel about the status of this backup. If for some reason this request
	// fails, delete the archive from the daemon and return that error up the chain to the caller.
	if notifyError := b.NotifyPanel(r, true); notifyError != nil {
		b.Remove()

		return notifyError
	}

	// Emit an event over the socket so we can update the backup in realtime on
	// the frontend for the server.
	s.Events().PublishJson(BackupCompletedEvent+":"+b.Uuid, map[string]interface{}{
		"uuid":        b.Uuid,
		"sha256_hash": r.Checksum,
		"file_size":   r.Size,
	})

	return nil
}