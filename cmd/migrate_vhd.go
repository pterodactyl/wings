package cmd

import (
	"context"
	"os"
	"os/exec"
	"strings"

	"emperror.dev/errors"
	"github.com/apex/log"
	"github.com/pterodactyl/wings/config"
	"github.com/pterodactyl/wings/internal/vhd"
	"github.com/pterodactyl/wings/loggers/cli"
	"github.com/pterodactyl/wings/remote"
	"github.com/pterodactyl/wings/server"
	"github.com/pterodactyl/wings/server/filesystem"
	"github.com/spf13/cobra"
)

type MigrateVHDCommand struct {
	manager *server.Manager
}

func newMigrateVHDCommand() *cobra.Command {
	return &cobra.Command{
		Use: "migrate-vhd",
		Short: "migrates existing data from a directory tree into virtual hard-disks",
		PreRun: func(cmd *cobra.Command, args []string) {
			log.SetLevel(log.DebugLevel)
			log.SetHandler(cli.Default)
		},
		Run: func(cmd *cobra.Command, args []string) {
			client := remote.NewFromConfig(config.Get())
			manager, err := server.NewManager(cmd.Context(), client, true)
			if err != nil {
				log.WithField("error", err).Fatal("failed to create new server manager")
			}
			c := &MigrateVHDCommand{
				manager: manager,
			}
			if err := c.Run(cmd.Context()); err != nil {
				log.WithField("error", err).Fatal("failed to execute command")
			}
		},
	}
}

// Run executes the migration command.
func (m *MigrateVHDCommand) Run(ctx context.Context) error {
	if !config.Get().System.UseVirtualDisks {
		return errors.New("cannot migrate to vhd: configuration file \"system.use_virtual_disks\" value is set to \"false\"")
	}
	for _, s := range m.manager.All() {
		s.Log().Debug("starting migration of server contents to virtual disk...")

		v := vhd.New(s.DiskSpace(), filesystem.VirtualDiskPath(s.Id()), s.Filesystem().Path())
		s.Log().WithField("disk_image", v.Path()).Info("creating virtual disk for server")
		if err := v.Allocate(ctx); err != nil {
			return errors.WithStackIf(err)
		}

		s.Log().Info("creating virtual filesystem for server")
		if err := v.MakeFilesystem(ctx); err != nil {
			// If the filesystem already exists no worries, just move on with our
			// day here.
			if !errors.Is(err, vhd.ErrFilesystemExists) {
				return errors.WithStack(err)
			}
		}

		bak := strings.TrimSuffix(s.Filesystem().Path(), "/") + "_bak"
		mounted, err := v.IsMounted(ctx)
		if err != nil {
			return err
		} else if !mounted {
			s.Log().WithField("backup_dir", bak).Debug("virtual disk is not yet mounted, creating backup directory")
			// Create a backup directory of the server files if one does not already exist
			// at that location. If one does exists we'll just assume it is good to go and
			// rely on it to provide the files we'll need.
			if _, err := os.Lstat(bak); os.IsNotExist(err) {
				if err := os.Rename(s.Filesystem().Path(), bak); err != nil {
					return errors.Wrap(err, "failed to rename existing data directory for backup")
				}
			} else if err != nil {
				return errors.WithStack(err)
			}
			if err := os.RemoveAll(s.Filesystem().Path()); err != nil && !os.IsNotExist(err) {
				return errors.Wrap(err, "failed to remove base server files path")
			}
		} else {
			s.Log().Warn("server appears to already have existing mount, not creating data backup")
		}

		// Attempt to mount the disk at the expected path now that we've created
		// a backup of the server files.
		if err := v.Mount(ctx); err != nil && !errors.Is(err, vhd.ErrFilesystemMounted) {
			return errors.WithStackIf(err)
		}

		// Copy over the files from the backup for this server but only
		// if we have a backup directory currently.
		_, err = os.Lstat(bak)
		if err != nil {
			if !os.IsNotExist(err) {
				s.Log().WithField("error", err).Warn("failed to stat backup directory")
			} else {
				s.Log().Info("no backup data directory exists, not restoring files")
			}
		} else {
			cmd := exec.CommandContext(ctx, "cp", "-r", bak+"/.", s.Filesystem().Path())
			if err := cmd.Run(); err != nil {
				return errors.Wrap(err, "migrate: failed to move old server files into new direcotry")
			} else {
				if err := os.RemoveAll(bak); err != nil {
					s.Log().WithField("directory", bak).WithField("error", err).Warn("failed to remove backup directory")
				}
			}
		}

		s.Log().Info("updating server file ownership...")
		if err := s.Filesystem().Chown("/"); err != nil {
			s.Log().WithField("error", err).Warn("failed to update ownership of new server files")
		}

		s.Log().Info("finished migration to virtual disk...")
	}
	return nil
}