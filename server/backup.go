package server

import (
	"emperror.dev/errors"
	"github.com/apex/log"
	"github.com/pterodactyl/wings/api"
	"github.com/pterodactyl/wings/server/backup"
	"io/ioutil"
	"os"
	"path"
)

// Notifies the panel of a backup's state and returns an error if one is encountered
// while performing this action.
func (s *Server) notifyPanelOfBackup(uuid string, ad *backup.ArchiveDetails, successful bool) error {
	if err := api.New().SendBackupStatus(uuid, ad.ToRequest(successful)); err != nil {
		if !api.IsRequestError(err) {
			s.Log().WithFields(log.Fields{
				"backup": uuid,
				"error":  err,
			}).Error("failed to notify panel of backup status due to wings error")

			return err
		}

		return errors.New(err.Error())
	}

	return nil
}

// Get all of the ignored files for a server based on its .pteroignore file in the root.
func (s *Server) getServerwideIgnoredFiles() (string, error) {
	p := path.Join(s.Filesystem().Path(), ".pteroignore")

	// Stat the file and don't resolve any symlink targets.
	stat, err := os.Lstat(p)
	if err != nil {
		if !os.IsNotExist(err) {
			return "", err
		}

		return "", nil
	}

	// Do not read symlinks.
	if stat.Mode()&os.ModeSymlink != 0 {
		return "", nil
	}

	// If the file is bigger than 32 KiB, don't read it at all.
	if stat.Size() > 32*1024 {
		return "", nil
	}

	f, err := os.Open(p)
	if err != nil {
		if !os.IsNotExist(err) {
			return "", err
		}

		return "", nil
	}

	// Read the entire file into memory.
	b, err := ioutil.ReadAll(f)
	if err != nil {
		return "", err
	}

	return string(b), nil
}

// Performs a server backup and then emits the event over the server websocket. We
// let the actual backup system handle notifying the panel of the status, but that
// won't emit a websocket event.
func (s *Server) Backup(b backup.BackupInterface) error {
	ignored := b.Ignored()
	if b.Ignored() == "" {
		if i, err := s.getServerwideIgnoredFiles(); err != nil {
			log.WithField("server", s.Id()).WithField("error", err).Warn("failed to get server-wide ignored files")
		} else {
			ignored = i
		}
	}

	ad, err := b.Generate(s.Filesystem().Path(), ignored)
	if err != nil {
		if notifyError := s.notifyPanelOfBackup(b.Identifier(), &backup.ArchiveDetails{}, false); notifyError != nil {
			s.Log().WithFields(log.Fields{
				"backup": b.Identifier(),
				"error":  notifyError,
			}).Warn("failed to notify panel of failed backup state")
		} else {
			s.Log().WithFields(log.Fields{
				"backup": b.Identifier(),
				"error":  err,
			}).Info("notified panel of failed backup state")
		}

		_ = s.Events().PublishJson(BackupCompletedEvent+":"+b.Identifier(), map[string]interface{}{
			"uuid":          b.Identifier(),
			"is_successful": false,
			"checksum":      "",
			"checksum_type": "sha1",
			"file_size":     0,
		})

		return errors.WithMessage(err, "backup: error while generating server backup")
	}

	// Try to notify the panel about the status of this backup. If for some reason this request
	// fails, delete the archive from the daemon and return that error up the chain to the caller.
	if notifyError := s.notifyPanelOfBackup(b.Identifier(), ad, true); notifyError != nil {
		_ = b.Remove()

		s.Log().WithField("error", notifyError).Info("failed to notify panel of successful backup state")
		return err
	} else {
		s.Log().WithField("backup", b.Identifier()).Info("notified panel of successful backup state")
	}

	// Emit an event over the socket so we can update the backup in realtime on
	// the frontend for the server.
	_ = s.Events().PublishJson(BackupCompletedEvent+":"+b.Identifier(), map[string]interface{}{
		"uuid":          b.Identifier(),
		"is_successful": true,
		"checksum":      ad.Checksum,
		"checksum_type": "sha1",
		"file_size":     ad.Size,
	})

	return nil
}
