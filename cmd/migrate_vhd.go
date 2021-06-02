package cmd

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"emperror.dev/errors"
	"github.com/apex/log"
	"github.com/pterodactyl/wings/config"
	"github.com/pterodactyl/wings/internal/vhd"
	"github.com/pterodactyl/wings/loggers/cli"
	"github.com/pterodactyl/wings/remote"
	"github.com/pterodactyl/wings/server"
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
			manager, err := server.NewManager(cmd.Context(), client)
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
	root := filepath.Join(config.Get().System.Data, ".disks")
	if err := os.MkdirAll(root, 0600); err != nil {
		return errors.Wrap(err, "failed to create root directory for virtual disks")
	}

	for _, s := range m.manager.All() {
		s.Log().Debug("starting migration of server contents to virtual disk...")

		v := s.Filesystem().NewVHD()
		if err := v.Allocate(ctx); err != nil {
			return errors.WithStackIf(err)
		}

		if err := v.MakeFilesystem(ctx); err != nil {
			// If the filesystem already exists no worries, just move on with our
			// day here.
			if !errors.Is(err, vhd.ErrFilesystemExists) {
				return errors.WithStack(err)
			}
		}

		bak := strings.TrimSuffix(s.Filesystem().Path(), "/") + "_bak"
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

		// Attempt to mount the disk at the expected path now that we've created
		// a backup of the server files.
		if err := v.Mount(ctx); err != nil && !errors.Is(err, vhd.ErrFilesystemMounted) {
			return errors.WithStackIf(err)
		}

		// Copy over the files from the backup for this server.
		cmd := exec.CommandContext(ctx, "cp", "-a", bak + "/.", s.Filesystem().Path())
		if err := cmd.Run(); err != nil {
			return errors.WithStack(err)
		}

		s.Log().Info("finished migration to virtual disk...")
	}
	return nil
}