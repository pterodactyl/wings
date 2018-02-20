package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/pterodactyl/wings/api"
	"github.com/pterodactyl/wings/command"
	"github.com/pterodactyl/wings/config"
	"github.com/pterodactyl/wings/constants"
	"github.com/pterodactyl/wings/control"
	"github.com/pterodactyl/wings/utils"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func main() {
	if err := command.RootCommand.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}

// RootCommand is the root command of wings
var RootCommand = &cobra.Command{
	Use:   "wings",
	Short: "",
	Long:  "",
	Run:   run,
}

var configPath string

func init() {
	RootCommand.Flags().StringVarP(&configPath, "config", "c", "./config.yml", "Allows to set the path of the configuration file.")
}

// Execute registers the RootCommand
func Execute() {
	RootCommand.Execute()
}

func run(cmd *cobra.Command, args []string) {
	utils.InitLogging()
	logrus.Info("Loading configuration...")
	if err := config.LoadConfiguration(configPath); err != nil {
		logrus.WithError(err).Fatal("Failed to find configuration file")
	}
	utils.ConfigureLogging()

	logrus.Info(`                     ____`)
	logrus.Info(`__ Pterodactyl _____/___/_______ _______ ______`)
	logrus.Info(`\_____\    \/\/    /   /       /  __   /   ___/`)
	logrus.Info(`   \___\          /   /   /   /  /_/  /___   /`)
	logrus.Info(`        \___/\___/___/___/___/___    /______/`)
	logrus.Info(`                            /_______/ v` + constants.Version)
	logrus.Info()

	logrus.Info("Configuration loaded successfully.")

	logrus.Info("Loading configured servers...")
	if err := control.LoadServerConfigurations(filepath.Join(viper.GetString(config.DataPath), constants.ServersPath)); err != nil {
		logrus.WithError(err).Error("Failed to load configured servers.")
	}
	if amount := len(control.GetServers()); amount == 1 {
		logrus.Info("Loaded 1 server.")
	} else {
		logrus.Info("Loaded " + strconv.Itoa(amount) + " servers.")
	}

	logrus.Info("Starting API Server...")
	a := api.NewAPI()
	a.Listen()
}
