package main

import (
	"flag"
	"fmt"
	"github.com/pterodactyl/wings/server"
	"go.uber.org/zap"
)

// Entrypoint for the Wings application. Configures the logger and checks any
// flags that were passed through in the boot arguments.
func main() {
	var configPath = *flag.String("config", "config.yml", "set the location for the configuration file")
	var debug = *flag.Bool("debug", false, "pass in order to run wings in debug mode")

	flag.Parse()

	zap.S().Infof("using configuration file: %s", configPath)

	c, err := ReadConfiguration(configPath)
	if err != nil {
		panic(err)
		return
	}

	if debug {
		c.Debug = true
	}

	printLogo()
	if err := configureLogging(c.Debug); err != nil {
		panic(err)
	}

	if c.Debug {
		zap.S().Debugw("running in debug mode")
	}

	servers, err := server.LoadDirectory("config/servers", *c.Docker)
	if err != nil {
		zap.S().Fatalw("failed to load server configurations", zap.Error(err))
		return
	}

	for _, s := range servers {
		zap.S().Infow("loaded configuration for server", zap.String("server", s.Uuid))
	}
}

// Configures the global logger for Zap so that we can call it from any location
// in the code without having to pass around a logger instance.
func configureLogging(debug bool) error {
	cfg := zap.NewProductionConfig()
	if debug {
		cfg = zap.NewDevelopmentConfig()
	}

	cfg.Encoding = "console"
	cfg.OutputPaths = []string{
		"stdout",
	}

	logger, err := cfg.Build()
	if err != nil {
		return err
	}

	zap.ReplaceGlobals(logger)

	return nil
}

// Prints the wings logo, nothing special here!
func printLogo() {
	fmt.Println()
	fmt.Println(`                     ____`)
	fmt.Println(`__ Pterodactyl _____/___/_______ _______ ______`)
	fmt.Println(`\_____\    \/\/    /   /       /  __   /   ___/`)
	fmt.Println(`   \___\          /   /   /   /  /_/  /___   /`)
	fmt.Println(`        \___/\___/___/___/___/___    /______/`)
	fmt.Println(`                            /_______/ v` + Version)
	fmt.Println()
}
