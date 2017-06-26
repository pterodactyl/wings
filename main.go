package main

import (
	log "github.com/Sirupsen/logrus"
	"github.com/schrej/wings.go/api"
	"github.com/schrej/wings.go/config"
	"github.com/schrej/wings.go/tools"
)

const (
	// Version of pterodactyld
	Version = "0.0.1-alpha"
)

func main() {
	tools.ConfigureLogging()

	log.Info("Starting wings.go version ", Version)

	// Load configuration
	log.Info("Loading configuration...")
	if err := config.LoadConfiguration(); err != nil {
		log.WithError(err).Fatal("Failed to find configuration file")
	}

	log.Info("Starting api webserver")
	api := api.NewAPI()
	api.Listen()
}
