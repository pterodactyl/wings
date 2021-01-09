package cmd

import (
	"errors"
	"fmt"
	"github.com/apex/log"
	"github.com/pterodactyl/wings/loggers/cli"
	"github.com/spf13/cobra"
	"runtime"
)

type upgrader struct{}

func newUpgradeCommand() *cobra.Command {
	u := upgrader{}
	command := &cobra.Command{
		Use:   "upgrade",
		Short: "Performs a self-upgrade for Wings.",
		Long: `Queries GitHub to find the latest Wings release and then downloads it, replacing
the existing system binary. This will use checksums and GPG signatures present on
the uploaded assets to validate that they have been released by the Pterodactyl team.

Once downloaded the Wings systemd process will be restarted if it is present on the
system, therefore this command MUST be executed as a root user.

This command can only be executed on ARM64/AMD64 Linux systems. All other systems will
report an error when executing this command.
`,
		PersistentPreRun: func(cmd *cobra.Command, args []string) {
			log.SetLevel(log.InfoLevel)
			if debug {
				log.SetLevel(log.DebugLevel)
			}
			log.SetHandler(cli.Default)
		},
		PreRunE: func(cmd *cobra.Command, args []string) error {
			if runtime.GOOS != "linux" {
				return errors.New(fmt.Sprintf("upgrade: os not supported: %s", runtime.GOOS))
			}
			if runtime.GOARCH != "arm64" && runtime.GOARCH != "amd64" {
				return errors.New(fmt.Sprintf("upgrade: unexpected architecture: %s", runtime.GOARCH))
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return u.execute()
		},
	}

	command.PersistentFlags().String("version", "latest", "download a specific version of Wings")
	command.PersistentFlags().String("repository", "pterodactyl/wings", "the repository to use when looking for updates -- if set, GPG verification is skipped")
	command.PersistentFlags().String("auth-token", "", "a GitHub authentication token to use for private repositories")
	command.PersistentFlags().Bool("download-only", false, "if set, do not restart wings after downloading")

	return command
}

// Executes a self-upgrade of Wings by pulling down the latest version from GitHub
// (or the given flag version) and then restarting the Wings process.
func (u *upgrader) execute() error {
	return nil
}
