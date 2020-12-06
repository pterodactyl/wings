package server

import (
	"bufio"
	"github.com/apex/log"
	"github.com/pkg/errors"
	"github.com/pterodactyl/wings/api"
	"github.com/pterodactyl/wings/server/backup"
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
func (s *Server) getServerwideIgnoredFiles() ([]string, error) {
	var ignored []string

	f, err := os.Open(path.Join(s.Filesystem().Path(), ".pteroignore"))
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
			s.Log().WithField("error", err).Warn("failed to retrieve ignored files listing for server")
		} else {
			ignored = i
		}
	}

	// Get the included files based on the root path and the ignored files provided.
	return s.Filesystem().GetIncludedFiles(s.Filesystem().Path(), ignored)
}

// Performs a server backup and then emits the event over the server websocket. We
// let the actual backup system handle notifying the panel of the status, but that
// won't emit a websocket event.
func (s *Server) Backup(b backup.BackupInterface) error {
	// Get the included files based on the root path and the ignored files provided.
	inc, err := s.GetIncludedBackupFiles(b.Ignored())
	if err != nil {
		return err
	}

	ad, err := b.Generate(inc, s.Filesystem().Path())
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

		s.Events().PublishJson(BackupCompletedEvent+":"+b.Identifier(), map[string]interface{}{
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
		b.Remove()
		s.Log().WithField("error", notifyError).Info("failed to notify panel of successful backup state")
		return err
	} else {
		s.Log().WithField("backup", b.Identifier()).Info("notified panel of successful backup state")
	}

	// Emit an event over the socket so we can update the backup in realtime on
	// the frontend for the server.
	s.Events().PublishJson(BackupCompletedEvent+":"+b.Identifier(), map[string]interface{}{
		"uuid":          b.Identifier(),
		"is_successful": true,
		"checksum":      ad.Checksum,
		"checksum_type": "sha1",
		"file_size":     ad.Size,
	})

	return nil
}
