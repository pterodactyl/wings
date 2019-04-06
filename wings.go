package main

import (
	"flag"
	"fmt"
	"github.com/pterodactyl/wings/server"
	"go.uber.org/zap"
	"net/http"
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
		zap.S().Infow("ensuring envrionment exists", zap.String("server", s.Uuid))

		if err := s.CreateEnvironment(); err != nil {
			zap.S().Errorw("failed to create an environment for server", zap.String("server", s.Uuid), zap.Error(err))
		}
	}

	r := &Router{
		Servers: servers,
	}

	router := r.ConfigureRouter()
	zap.S().Infow("configuring webserver", zap.String("host", c.Api.Host), zap.Int("port", c.Api.Port))
	if err := http.ListenAndServe(fmt.Sprintf("%s:%d", c.Api.Host, c.Api.Port), router); err != nil {
		zap.S().Fatalw("failed to configure HTTP server", zap.Error(err))
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
