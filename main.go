package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/pterodactyl/wings/api"
	"github.com/pterodactyl/wings/config"
	"github.com/pterodactyl/wings/constants"
	"github.com/pterodactyl/wings/control"
	"github.com/pterodactyl/wings/utils"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var configPath string
var RootCommand = &cobra.Command{
	Use:   "wings",
	Short: "Wings is the next generation server control daemon for Pterodactyl",
	Long:  "Wings is the next generation server control daemon for Pterodactyl",
	Run:   run,
}

// Entrypoint of the application. Currently just boots up the cobra command
// and lets that handle everything else.
func main() {
	RootCommand.Flags().StringVarP(&configPath, "config", "c", "./config.yml", "Allows to set the path of the configuration file.")

	if err := RootCommand.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}

// Bootstraps the application and beging the process of running the API and
// server instances.
func run(cmd *cobra.Command, args []string) {
	utils.InitLogging()

	logrus.Info("Booting configuration file...")
	if err := config.LoadConfiguration(configPath); err != nil {
		logrus.WithError(err).Fatal("Could not locate a suitable config.yml file for this Daemon.")
	}

	logrus.Info("Configuration successfully loaded, booting application.")
	utils.ConfigureLogging()

	logrus.Info(`                     ____`)
	logrus.Info(`__ Pterodactyl _____/___/_______ _______ ______`)
	logrus.Info(`\_____\    \/\/    /   /       /  __   /   ___/`)
	logrus.Info(`   \___\          /   /   /   /  /_/  /___   /`)
	logrus.Info(`        \___/\___/___/___/___/___    /______/`)
	logrus.Info(`                            /_______/ v` + constants.Version)
	logrus.Info()

	logrus.Info("Loading configured servers.")
	if err := control.LoadServerConfigurations(filepath.Join(viper.GetString(config.DataPath), constants.ServersPath)); err != nil {
		logrus.WithError(err).Error("Failed to load configured servers.")
	}

	if amount := len(control.GetServers()); amount == 1 {
		logrus.Info("Found and loaded " + strconv.Itoa(amount) + " server(s).")
	}

	logrus.Info("Registering API server and booting.")
	a := api.InternalAPI{}
	a.Listen()
}
