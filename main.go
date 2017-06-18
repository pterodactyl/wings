package main

import (
	log "github.com/Sirupsen/logrus"
	"github.com/schrej/pterodactyld/config"
	"github.com/schrej/pterodactyld/tools"
)

const (
	// Version of pterodactyld
	Version = "0.0.1-alpha"
)

func main() {
	tools.ConfigureLogging()

	log.Info("Starting pterodactyld version ", Version)

	// Load configuration
	log.Info("Loading configuration...")
	if err := config.LoadConfiguration(); err != nil {
		log.WithError(err).Fatal("Failed to find configuration file")
	}
}
