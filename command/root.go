package command

import (
	"github.com/Pterodactyl/wings/api"
	"github.com/Pterodactyl/wings/config"
	"github.com/Pterodactyl/wings/tools"
	log "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
)

const (
	// Version of pterodactyld
	Version = "0.0.1-alpha"
)

var RootCommand = &cobra.Command{
	Use:   "wings",
	Short: "",
	Long:  "",
	Run:   run,
}

func init() {

}

func Execute() {
	RootCommand.Execute()
}

func run(cmd *cobra.Command, args []string) {
	tools.InitLogging()
	log.Info("Loading configuration")
	if err := config.LoadConfiguration(nil); err != nil {
		log.WithError(err).Fatal("Failed to find configuration file")
	}
	tools.ConfigureLogging()

	log.Info(`                     ____`)
	log.Info(`__ Pterodactyl _____/___/_______ _______ ______`)
	log.Info(`\_____\    \/\/    /   /       /  __   /   ___/`)
	log.Info(`   \___\          /   /   /   /  /_/  /___   /`)
	log.Info(`        \___/\___/___/___/___/___    /______/`)
	log.Info(`                            /_______/ v` + Version)
	log.Info()

	log.Info("Configuration loaded successfully.")

	log.Info("Starting api webserver")
	api := api.NewAPI()
	api.Listen()
}
