package main

import (
	"flag"
	"fmt"
	"github.com/gorilla/websocket"
	"github.com/pterodactyl/wings/config"
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

	c, err := config.ReadConfiguration(configPath)
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

	zap.S().Infof("checking for pterodactyl system user \"%s\"", c.System.User)
	if su, err := c.EnsurePterodactylUser(); err != nil {
		zap.S().Panicw("failed to create pterodactyl system user", zap.Error(err))
		return
	} else {
		zap.S().Infow("configured system user", zap.String("username", su.Username), zap.String("uid", su.Uid), zap.String("gid", su.Gid))
	}

	zap.S().Infow("beginnning file permission setting on server data directories")
	if err := c.EnsureFilePermissions(); err != nil {
		zap.S().Errorw("failed to properly chown data directories", zap.Error(err))
	} else {
		zap.S().Infow("finished ensuring file permissions")
	}

	servers, err := server.LoadDirectory("data/servers", c.System)
	if err != nil {
		zap.S().Fatalw("failed to load server configurations", zap.Error(err))
		return
	}

	for _, s := range servers {
		zap.S().Infow("loaded configuration for server", zap.String("server", s.Uuid))
		zap.S().Infow("ensuring envrionment exists", zap.String("server", s.Uuid))

		if err := s.Environment.Create(); err != nil {
			zap.S().Errorw("failed to create an environment for server", zap.String("server", s.Uuid), zap.Error(err))
		}

		if r, err := s.Environment.IsRunning(); err != nil {
			zap.S().Errorw("error checking server environment status", zap.String("server", s.Uuid), zap.Error(err))
		} else if r {
			zap.S().Infow("detected server is running, re-attaching to process", zap.String("server", s.Uuid))
			s.SetState(server.ProcessRunningState)
			if err := s.Environment.Attach(); err != nil {
				zap.S().Errorw("error attaching to server environment", zap.String("server", s.Uuid), zap.Error(err))
				s.SetState(server.ProcessOfflineState)
			}
		}
	}

	r := &Router{
		Servers: servers,
		token:   c.AuthenticationToken,
		upgrader: websocket.Upgrader{
			// Ensure that the websocket request is originating from the Panel itself,
			// and not some other location.
			CheckOrigin: func(r *http.Request) bool {
				return r.Header.Get("Origin") == c.PanelLocation
			},
		},
	}

	router := r.ConfigureRouter()
	zap.S().Infow("configuring webserver", zap.Bool("ssl", c.Api.Ssl.Enabled), zap.String("host", c.Api.Host), zap.Int("port", c.Api.Port))

	addr := fmt.Sprintf("%s:%d", c.Api.Host, c.Api.Port)
	if c.Api.Ssl.Enabled {
		if err := http.ListenAndServeTLS(addr, c.Api.Ssl.CertificateFile, c.Api.Ssl.KeyFile, router); err != nil {
			zap.S().Fatalw("failed to configure HTTPS server", zap.Error(err))
		}
	} else {
		if err := http.ListenAndServe(addr, router); err != nil {
			zap.S().Fatalw("failed to configure HTTP server", zap.Error(err))
		}
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
