package main

import (
	"crypto/tls"
	"flag"
	"fmt"
	"github.com/gorilla/websocket"
	"github.com/pkg/errors"
	"github.com/pterodactyl/wings/config"
	"github.com/pterodactyl/wings/server"
	"github.com/pterodactyl/wings/sftp"
	"github.com/remeh/sizedwaitgroup"
	"go.uber.org/zap"
	"net/http"
	"os"
)

var configPath = "config.yml"
var debug = false

// Entrypoint for the Wings application. Configures the logger and checks any
// flags that were passed through in the boot arguments.
func main() {
	flag.StringVar(&configPath, "config", "config.yml", "set the location for the configuration file")
	flag.BoolVar(&debug, "debug", false, "pass in order to run wings in debug mode")

	flag.Parse()

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

	zap.S().Infof("using configuration from path: %s", configPath)
	if c.Debug {
		zap.S().Debugw("running in debug mode")
		zap.S().Infow("certificate checking is disabled")

		http.DefaultTransport.(*http.Transport).TLSClientConfig = &tls.Config{
			InsecureSkipVerify: true,
		}
	}

	config.Set(c)
	config.SetDebugViaFlag(debug)

	zap.S().Infof("checking for pterodactyl system user \"%s\"", c.System.Username)
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

	if err := server.LoadDirectory("data/servers", &c.System); err != nil {
		zap.S().Fatalw("failed to load server configurations", zap.Error(errors.WithStack(err)))
		return
	}

	if err := ConfigureDockerEnvironment(&c.Docker); err != nil {
		zap.S().Fatalw("failed to configure docker environment", zap.Error(errors.WithStack(err)))
		os.Exit(1)
	}

	if err := c.WriteToDisk(); err != nil {
		zap.S().Errorw("failed to save configuration to disk", zap.Error(errors.WithStack(err)))
	}

	// Just for some nice log output.
	for _, s := range server.GetServers().All() {
		zap.S().Infow("loaded configuration for server", zap.String("server", s.Uuid))
	}

	// Create a new WaitGroup that limits us to 4 servers being bootstrapped at a time
	// on Wings. This allows us to ensure the environment exists, write configurations,
	// and reboot processes without causing a slow-down due to sequential booting.
	wg := sizedwaitgroup.New(4)

	for _, serv := range server.GetServers().All() {
		wg.Add()

		go func(s *server.Server) {
			defer wg.Done()

			// Create a server environment if none exists currently. This allows us to recover from Docker
			// being reinstalled on the host system for example.
			zap.S().Infow("ensuring envrionment exists", zap.String("server", s.Uuid))
			if err := s.Environment.Create(); err != nil {
				zap.S().Errorw("failed to create an environment for server", zap.String("server", s.Uuid), zap.Error(err))
			}

			r, err := s.Environment.IsRunning()
			if err != nil {
				zap.S().Errorw("error checking server environment status", zap.String("server", s.Uuid), zap.Error(err))
			}

			// If the server is currently running on Docker, mark the process as being in that state.
			// We never want to stop an instance that is currently running external from Wings since
			// that is a good way of keeping things running even if Wings gets in a very corrupted state.
			//
			// This will also validate that a server process is running if the last tracked state we have
			// is that it was running, but we see that the container process is not currently running.
			if r || (!r && (s.State == server.ProcessRunningState || s.State == server.ProcessStartingState)) {
				zap.S().Infow("detected server is running, re-attaching to process", zap.String("server", s.Uuid))
				if err := s.Environment.Start(); err != nil {
					zap.S().Warnw(
						"failed to properly start server detected as already running",
						zap.String("server", s.Uuid),
						zap.Error(errors.WithStack(err)),
					)
				}

				return
			}

			// Addresses potentially invalid data in the stored file that can cause Wings to lose
			// track of what the actual server state is.
			s.SetState(server.ProcessOfflineState)
		}(serv)
	}

	// Wait until all of the servers are ready to go before we fire up the HTTP server.
	wg.Wait()

	// If the SFTP subsystem should be started, do so now.
	if c.System.Sftp.UseInternalSystem {
		sftp.Initialize(c)
	}

	r := &Router{
		token: c.AuthenticationToken,
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
