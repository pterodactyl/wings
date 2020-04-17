package cmd

import (
	"crypto/tls"
	"fmt"
	"net/http"
	"os"
	"path"
	"strings"

	"github.com/pkg/errors"
	"github.com/pkg/profile"
	"github.com/pterodactyl/wings/config"
	"github.com/pterodactyl/wings/environment"
	"github.com/pterodactyl/wings/router"
	"github.com/pterodactyl/wings/server"
	"github.com/pterodactyl/wings/sftp"
	"github.com/pterodactyl/wings/system"
	"github.com/remeh/sizedwaitgroup"
	"github.com/spf13/cobra"
	"go.uber.org/zap"
)

var configPath = config.DefaultLocation
var debug = false
var shouldRunProfiler = false

var root = &cobra.Command{
	Use:   "wings",
	Short: "The wings of the pterodactyl game management panel",
	Long:  ``,
	Run:   rootCmdRun,
}

func init() {
	root.PersistentFlags().StringVar(&configPath, "config", config.DefaultLocation, "set the location for the configuration file")
	root.PersistentFlags().BoolVar(&debug, "debug", false, "pass in order to run wings in debug mode")
	root.PersistentFlags().BoolVar(&shouldRunProfiler, "profile", false, "pass in order to profile wings")

	root.AddCommand(configureCmd)
}

// Get the configuration path based on the arguments provided.
func readConfiguration() (*config.Configuration, error) {
	var p = configPath
	if !strings.HasPrefix(p, "/") {
		d, err := os.Getwd()
		if err != nil {
			return nil, err
		}

		p = path.Clean(path.Join(d, configPath))
	}

	if s, err := os.Stat(p); err != nil {
		return nil, errors.WithStack(err)
	} else if s.IsDir() {
		return nil, errors.New("cannot use directory as configuration file path")
	}

	return config.ReadConfiguration(p)
}

func rootCmdRun(*cobra.Command, []string) {
	// Profile wings in production!!!!
	if shouldRunProfiler {
		defer profile.Start().Stop()
	}

	c, err := readConfiguration()
	if err != nil {
		panic(err)
	}

	if debug {
		c.Debug = true
	}

	printLogo()
	if err := configureLogging(c.Debug); err != nil {
		panic(err)
	}

	zap.S().Infof("using configuration from path: %s", c.GetPath())
	if c.Debug {
		zap.S().Debugw("running in debug mode")
		zap.S().Infow("certificate checking is disabled")

		http.DefaultTransport.(*http.Transport).TLSClientConfig = &tls.Config{
			InsecureSkipVerify: true,
		}
	}

	config.Set(c)
	config.SetDebugViaFlag(debug)

	if err := c.System.ConfigureDirectories(); err != nil {
		zap.S().Panicw("failed to configure system directories for pterodactyl", zap.Error(err))
		return
	}

	zap.S().Infof("checking for pterodactyl system user \"%s\"", c.System.Username)
	if su, err := c.EnsurePterodactylUser(); err != nil {
		zap.S().Panicw("failed to create pterodactyl system user", zap.Error(err))
		return
	} else {
		zap.S().Infow("configured system user", zap.String("username", su.Username), zap.String("uid", su.Uid), zap.String("gid", su.Gid))
	}

	zap.S().Infow("beginning file permission setting on server data directories")
	if err := c.EnsureFilePermissions(); err != nil {
		zap.S().Errorw("failed to properly chown data directories", zap.Error(err))
	} else {
		zap.S().Infow("finished ensuring file permissions")
	}

	if err := server.LoadDirectory(); err != nil {
		zap.S().Fatalw("failed to load server configurations", zap.Error(errors.WithStack(err)))
		return
	}

	if err := environment.ConfigureDocker(&c.Docker); err != nil {
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
			zap.S().Infow("ensuring environment exists", zap.String("server", s.Uuid))
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
			if r || (!r && s.IsRunning()) {
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

	// Ensure the archive directory exists.
	if err := os.MkdirAll(c.System.ArchiveDirectory, 0755); err != nil {
		zap.S().Errorw("failed to create archive directory", zap.Error(err))
	}

	// Ensure the backup directory exists.
	if err := os.MkdirAll(c.System.BackupDirectory, 0755); err != nil {
		zap.S().Errorw("failed to create backup directory", zap.Error(err))
	}

	zap.S().Infow("configuring webserver", zap.Bool("ssl", c.Api.Ssl.Enabled), zap.String("host", c.Api.Host), zap.Int("port", c.Api.Port))

	r := router.Configure()
	addr := fmt.Sprintf("%s:%d", c.Api.Host, c.Api.Port)

	if c.Api.Ssl.Enabled {
		if err := r.RunTLS(addr, c.Api.Ssl.CertificateFile, c.Api.Ssl.KeyFile); err != nil {
			zap.S().Fatalw("failed to configure HTTPS server", zap.Error(err))
		}
	} else {
		if err := r.Run(addr); err != nil {
			zap.S().Fatalw("failed to configure HTTP server", zap.Error(err))
		}
	}

	// r := &Router{
	// 	token: c.AuthenticationToken,
	// 	upgrader: websocket.Upgrader{
	// 		// Ensure that the websocket request is originating from the Panel itself,
	// 		// and not some other location.
	// 		CheckOrigin: func(r *http.Request) bool {
	// 			return r.Header.Get("Origin") == c.PanelLocation
	// 		},
	// 	},
	// }
}

// Execute calls cobra to handle cli commands
func Execute() error {
	return root.Execute()
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
	fmt.Println(`                            /_______/ v` + system.Version)
	fmt.Println()
	fmt.Println(`Website: https://pterodactyl.io`)
	fmt.Println(`Source: https://github.com/pterodactyl/wings`)
	fmt.Println()
	fmt.Println(`Copyright © 2018 - 2020 Dane Everitt & Contributors`)
	fmt.Println()
}
