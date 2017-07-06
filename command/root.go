package command

import (
	"github.com/Pterodactyl/wings/api"
	"github.com/Pterodactyl/wings/config"
	"github.com/Pterodactyl/wings/constants"
	"github.com/Pterodactyl/wings/tools"
	log "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
)

// RootCommand is the root command of wings
var RootCommand = &cobra.Command{
	Use:   "wings",
	Short: "",
	Long:  "",
	Run:   run,
}

func init() {

}

// Execute registers the RootCommand
func Execute() {
	RootCommand.Execute()
}

func run(cmd *cobra.Command, args []string) {
	tools.InitLogging()
	log.Info("Loading configuration")
	if err := config.LoadConfiguration(""); err != nil {
		log.WithError(err).Fatal("Failed to find configuration file")
	}
	tools.ConfigureLogging()

	log.Info(`                     ____`)
	log.Info(`__ Pterodactyl _____/___/_______ _______ ______`)
	log.Info(`\_____\    \/\/    /   /       /  __   /   ___/`)
	log.Info(`   \___\          /   /   /   /  /_/  /___   /`)
	log.Info(`        \___/\___/___/___/___/___    /______/`)
	log.Info(`                            /_______/ v` + constants.Version)
	log.Info()

	log.Info("Configuration loaded successfully.")

	log.Info("Starting api webserver")
	api := api.NewAPI()
	api.Listen()
}
