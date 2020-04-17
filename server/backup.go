package server

import (
	"github.com/pkg/errors"
	"github.com/pterodactyl/wings/api"
	"github.com/pterodactyl/wings/server/backup"
	"go.uber.org/zap"
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
	if err := b.Backup(s.Filesystem.Path()); err != nil {
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
