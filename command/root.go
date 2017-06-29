package command

import (
	"fmt"

	"github.com/schrej/wings/api"
	"github.com/schrej/wings/config"
	"github.com/schrej/wings/tools"
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
	fmt.Println("Loading configuration")
	if err := config.LoadConfiguration(nil); err != nil {
		log.WithError(err).Fatal("Failed to find configuration file")
	}
	tools.ConfigureLogging()

	log.Info("Starting wings.go version ", Version)

	// Load configuration
	log.Info("Loading configuration...")

	log.Info("Starting api webserver")
	api := api.NewAPI()
	api.Listen()
}
