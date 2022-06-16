package server

import (
	"io"
	"io/fs"
	"os"
	"time"

	"emperror.dev/errors"
	"github.com/apex/log"
	"github.com/docker/docker/client"

	"github.com/pterodactyl/wings/environment"
	"github.com/pterodactyl/wings/remote"
	"github.com/pterodactyl/wings/server/backup"
)

// Notifies the panel of a backup's state and returns an error if one is encountered
// while performing this action.
func (s *Server) notifyPanelOfBackup(uuid string, ad *backup.ArchiveDetails, successful bool) error {
	if err := s.client.SetBackupStatus(s.Context(), uuid, ad.ToRequest(successful)); err != nil {
		if !remote.IsRequestError(err) {
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
	f, st, err := s.Filesystem().File(".pteroignore")
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", nil
		}
		return "", err
	}
	defer f.Close()
	if st.Mode()&os.ModeSymlink != 0 || st.Size() > 32*1024 {
		// Don't read a symlinked ignore file, or a file larger than 32KiB in size.
		return "", nil
	}
	b, err := io.ReadAll(f)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// Backup performs a server backup and then emits the event over the server
// websocket. We let the actual backup system handle notifying the panel of the
// status, but that won't emit a websocket event.
func (s *Server) Backup(b backup.BackupInterface) error {
	ignored := b.Ignored()
	if b.Ignored() == "" {
		if i, err := s.getServerwideIgnoredFiles(); err != nil {
			log.WithField("server", s.ID()).WithField("error", err).Warn("failed to get server-wide ignored files")
		} else {
			ignored = i
		}
	}

	ad, err := b.Generate(s.Context(), s.Filesystem().Path(), ignored)
	if err != nil {
		if err := s.notifyPanelOfBackup(b.Identifier(), &backup.ArchiveDetails{}, false); err != nil {
			s.Log().WithFields(log.Fields{
				"backup": b.Identifier(),
				"error":  err,
			}).Warn("failed to notify panel of failed backup state")
		} else {
			s.Log().WithField("backup", b.Identifier()).Info("notified panel of failed backup state")
		}

		s.Events().Publish(BackupCompletedEvent+":"+b.Identifier(), map[string]interface{}{
			"uuid":          b.Identifier(),
			"is_successful": false,
			"checksum":      "",
			"checksum_type": "sha1",
			"file_size":     0,
		})

		return errors.WrapIf(err, "backup: error while generating server backup")
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
	s.Events().Publish(BackupCompletedEvent+":"+b.Identifier(), map[string]interface{}{
		"uuid":          b.Identifier(),
		"is_successful": true,
		"checksum":      ad.Checksum,
		"checksum_type": "sha1",
		"file_size":     ad.Size,
	})

	return nil
}

// RestoreBackup calls the Restore function on the provided backup. Once this
// restoration is completed an event is emitted to the websocket to notify the
// Panel that is has been completed.
//
// In addition to the websocket event an API call is triggered to notify the
// Panel of the new state.
func (s *Server) RestoreBackup(b backup.BackupInterface, reader io.ReadCloser) (err error) {
	s.Config().SetSuspended(true)
	// Local backups will not pass a reader through to this function, so check first
	// to make sure it is a valid reader before trying to close it.
	defer func() {
		s.Config().SetSuspended(false)
		if reader != nil {
			reader.Close()
		}
	}()
	// Send an API call to the Panel as soon as this function is done running so that
	// the Panel is informed of the restoration status of this backup.
	defer func() {
		if rerr := s.client.SendRestorationStatus(s.Context(), b.Identifier(), err == nil); rerr != nil {
			s.Log().WithField("error", rerr).WithField("backup", b.Identifier()).Error("failed to notify Panel of backup restoration status")
		}
	}()

	// Don't try to restore the server until we have completely stopped the running
	// instance, otherwise you'll likely hit all types of write errors due to the
	// server being suspended.
	if s.Environment.State() != environment.ProcessOfflineState {
		if err = s.Environment.WaitForStop(s.Context(), time.Minute*2, false); err != nil {
			if !client.IsErrNotFound(err) {
				return errors.WrapIf(err, "server/backup: restore: failed to wait for container stop")
			}
		}
	}

	// Attempt to restore the backup to the server by running through each entry
	// in the file one at a time and writing them to the disk.
	s.Log().Debug("starting file writing process for backup restoration")
	err = b.Restore(s.Context(), reader, func(file string, r io.Reader, mode fs.FileMode, atime, mtime time.Time) error {
		s.Events().Publish(DaemonMessageEvent, "(restoring): "+file)
		if err := s.Filesystem().Writefile(file, r); err != nil {
			return err
		}
		if err := s.Filesystem().Chmod(file, mode); err != nil {
			return err
		}
		return s.Filesystem().Chtimes(file, atime, mtime)
	})

	return errors.WithStackIf(err)
}
