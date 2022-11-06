package cmd

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	log2 "log"
	"net/http"
	_ "net/http/pprof"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/NYTimes/logrotate"
	"github.com/apex/log"
	"github.com/apex/log/handlers/multi"
	"github.com/docker/docker/client"
	"github.com/gammazero/workerpool"
	"github.com/mitchellh/colorstring"
	"github.com/spf13/cobra"
	"golang.org/x/crypto/acme"
	"golang.org/x/crypto/acme/autocert"

	"github.com/pterodactyl/wings/config"
	"github.com/pterodactyl/wings/environment"
	"github.com/pterodactyl/wings/internal/cron"
	"github.com/pterodactyl/wings/internal/database"
	"github.com/pterodactyl/wings/loggers/cli"
	"github.com/pterodactyl/wings/remote"
	"github.com/pterodactyl/wings/router"
	"github.com/pterodactyl/wings/server"
	"github.com/pterodactyl/wings/sftp"
	"github.com/pterodactyl/wings/system"
)

var (
	configPath = config.DefaultLocation
	debug      = false
)

var rootCommand = &cobra.Command{
	Use:   "wings",
	Short: "Runs the API server allowing programmatic control of game servers for Pterodactyl Panel.",
	PreRun: func(cmd *cobra.Command, args []string) {
		initConfig()
		initLogging()
		if tls, _ := cmd.Flags().GetBool("auto-tls"); tls {
			if host, _ := cmd.Flags().GetString("tls-hostname"); host == "" {
				fmt.Println("A TLS hostname must be provided when running wings with automatic TLS, e.g.:\n\n    ./wings --auto-tls --tls-hostname my.example.com")
				os.Exit(1)
			}
		}
	},
	Run: rootCmdRun,
}

var versionCommand = &cobra.Command{
	Use:   "version",
	Short: "Prints the current executable version and exits.",
	Run: func(cmd *cobra.Command, _ []string) {
		fmt.Printf("wings v%s\nCopyright © 2018 - %d Dane Everitt & Contributors\n", system.Version, time.Now().Year())
	},
}

func Execute() {
	if err := rootCommand.Execute(); err != nil {
		log2.Fatalf("failed to execute command: %s", err)
	}
}

func init() {
	rootCommand.PersistentFlags().StringVar(&configPath, "config", config.DefaultLocation, "set the location for the configuration file")
	rootCommand.PersistentFlags().BoolVar(&debug, "debug", false, "pass in order to run wings in debug mode")

	// Flags specifically used when running the API.
	rootCommand.Flags().Bool("pprof", false, "if the pprof profiler should be enabled. The profiler will bind to localhost:6060 by default")
	rootCommand.Flags().Int("pprof-block-rate", 0, "enables block profile support, may have performance impacts")
	rootCommand.Flags().Int("pprof-port", 6060, "If provided with --pprof, the port it will run on")
	rootCommand.Flags().Bool("auto-tls", false, "pass in order to have wings generate and manage its own SSL certificates using Let's Encrypt")
	rootCommand.Flags().String("tls-hostname", "", "required with --auto-tls, the FQDN for the generated SSL certificate")
	rootCommand.Flags().Bool("ignore-certificate-errors", false, "ignore certificate verification errors when executing API calls")

	rootCommand.AddCommand(versionCommand)
	rootCommand.AddCommand(configureCmd)
	rootCommand.AddCommand(newDiagnosticsCommand())
}

func rootCmdRun(cmd *cobra.Command, _ []string) {
	printLogo()
	log.Debug("running in debug mode")
	log.WithField("config_file", configPath).Info("loading configuration from file")

	if ok, _ := cmd.Flags().GetBool("ignore-certificate-errors"); ok {
		log.Warn("running with --ignore-certificate-errors: TLS certificate host chains and name will not be verified")
		http.DefaultTransport.(*http.Transport).TLSClientConfig = &tls.Config{
			InsecureSkipVerify: true,
		}
	}

	if err := config.ConfigureTimezone(); err != nil {
		log.WithField("error", err).Fatal("failed to detect system timezone or use supplied configuration value")
	}
	log.WithField("timezone", config.Get().System.Timezone).Info("configured wings with system timezone")
	if err := config.ConfigureDirectories(); err != nil {
		log.WithField("error", err).Fatal("failed to configure system directories for pterodactyl")
		return
	}
	if err := config.EnsurePterodactylUser(); err != nil {
		log.WithField("error", err).Fatal("failed to create pterodactyl system user")
	}
	log.WithFields(log.Fields{
		"username": config.Get().System.Username,
		"uid":      config.Get().System.User.Uid,
		"gid":      config.Get().System.User.Gid,
	}).Info("configured system user successfully")
	if err := config.EnableLogRotation(); err != nil {
		log.WithField("error", err).Fatal("failed to configure log rotation on the system")
		return
	}

	pclient := remote.New(
		config.Get().PanelLocation,
		remote.WithCredentials(config.Get().AuthenticationTokenId, config.Get().AuthenticationToken),
		remote.WithHttpClient(&http.Client{
			Timeout: time.Second * time.Duration(config.Get().RemoteQuery.Timeout),
		}),
	)

	if err := database.Initialize(); err != nil {
		log.WithField("error", err).Fatal("failed to initialize database")
	}

	manager, err := server.NewManager(cmd.Context(), pclient)
	if err != nil {
		log.WithField("error", err).Fatal("failed to load server configurations")
	}

	if err := environment.ConfigureDocker(cmd.Context()); err != nil {
		log.WithField("error", err).Fatal("failed to configure docker environment")
	}

	if err := config.WriteToDisk(config.Get()); err != nil {
		log.WithField("error", err).Fatal("failed to write configuration to disk")
	}

	// Just for some nice log output.
	for _, s := range manager.All() {
		log.WithField("server", s.ID()).Info("finished loading configuration for server")
	}

	states, err := manager.ReadStates()
	if err != nil {
		log.WithField("error", err).Error("failed to retrieve locally cached server states from disk, assuming all servers in offline state")
	}

	ticker := time.NewTicker(time.Minute)
	// Every minute, write the current server states to the disk to allow for a more
	// seamless hard-reboot process in which wings will re-sync server states based
	// on its last tracked state.
	go func() {
		for {
			select {
			case <-ticker.C:
				if err := manager.PersistStates(); err != nil {
					log.WithField("error", err).Warn("failed to persist server states to disk")
				}
			case <-cmd.Context().Done():
				ticker.Stop()
				return
			}
		}
	}()

	// Create a new workerpool that limits us to 4 servers being bootstrapped at a time
	// on Wings. This allows us to ensure the environment exists, write configurations,
	// and reboot processes without causing a slow-down due to sequential booting.
	pool := workerpool.New(4)
	for _, serv := range manager.All() {
		s := serv

		// For each server we encounter make sure the root data directory exists.
		if err := s.EnsureDataDirectoryExists(); err != nil {
			s.Log().Error("could not create root data directory for server: not loading server...")
			continue
		}

		pool.Submit(func() {
			s.Log().Info("configuring server environment and restoring to previous state")
			var st string
			if state, exists := states[s.ID()]; exists {
				st = state
			}

			// Use a timed context here to avoid booting issues where Docker hangs for a
			// specific container that would cause Wings to be un-bootable until the entire
			// machine is rebooted. It is much better for us to just have a single failed
			// server instance than an entire offline node.
			//
			// @see https://github.com/pterodactyl/panel/issues/2475
			// @see https://github.com/pterodactyl/panel/issues/3358
			ctx, cancel := context.WithTimeout(cmd.Context(), time.Second*30)
			defer cancel()

			r, err := s.Environment.IsRunning(ctx)
			// We ignore missing containers because we don't want to actually block booting of wings at this
			// point. If we didn't do this, and you pruned all the images and then started wings you could
			// end up waiting a long period of time for all the images to be re-pulled on Wings boot rather
			// than when the server itself is started.
			if err != nil && !client.IsErrNotFound(err) {
				s.Log().WithField("error", err).Error("error checking server environment status")
			}

			// Check if the server was previously running. If so, attempt to start the server now so that Wings
			// can pick up where it left off. If the environment does not exist at all, just create it and then allow
			// the normal flow to execute.
			//
			// This does mean that booting wings after a catastrophic machine crash and wiping out the Docker images
			// as a result will result in a slow boot.
			if !r && (st == environment.ProcessRunningState || st == environment.ProcessStartingState) {
				if err := s.HandlePowerAction(server.PowerActionStart); err != nil {
					s.Log().WithField("error", err).Warn("failed to return server to running state")
				}
			} else if r || (!r && s.IsRunning()) {
				// If the server is currently running on Docker, mark the process as being in that state.
				// We never want to stop an instance that is currently running external from Wings since
				// that is a good way of keeping things running even if Wings gets in a very corrupted state.
				//
				// This will also validate that a server process is running if the last tracked state we have
				// is that it was running, but we see that the container process is not currently running.
				s.Log().Info("detected server is running, re-attaching to process...")

				s.Environment.SetState(environment.ProcessRunningState)
				if err := s.Environment.Attach(ctx); err != nil {
					s.Log().WithField("error", err).Warn("failed to attach to running server environment")
				}
			} else {
				// At this point we've determined that the server should indeed be in an offline state, so we'll
				// make a call to set that state just to ensure we don't ever accidentally end up with some invalid
				// state being tracked.
				s.Environment.SetState(environment.ProcessOfflineState)
			}

			if state := s.Environment.State(); state == environment.ProcessStartingState || state == environment.ProcessRunningState {
				s.Log().Debug("re-syncing server configuration for already running server")
				if err := s.Sync(); err != nil {
					s.Log().WithError(err).Error("failed to re-sync server configuration")
				}
			}
		})
	}

	// Wait until all the servers are ready to go before we fire up the SFTP and HTTP servers.
	pool.StopWait()
	defer func() {
		// Cancel the context on all the running servers at this point, even though the
		// program is just shutting down.
		for _, s := range manager.All() {
			s.CtxCancel()
		}
	}()

	if s, err := cron.Scheduler(cmd.Context(), manager); err != nil {
		log.WithField("error", err).Fatal("failed to initialize cron system")
	} else {
		log.WithField("subsystem", "cron").Info("starting cron processes")
		s.StartAsync()
	}

	go func() {
		// Run the SFTP server.
		if err := sftp.New(manager).Run(); err != nil {
			log.WithError(err).Fatal("failed to initialize the sftp server")
			return
		}
	}()

	go func() {
		log.Info("updating server states on Panel: marking installing/restoring servers as normal")
		// Update all the servers on the Panel to be in a valid state if they're
		// currently marked as installing/restoring now that Wings is restarted.
		if err := pclient.ResetServersState(cmd.Context()); err != nil {
			log.WithField("error", err).Error("failed to reset server states on Panel: some instances may be stuck in an installing/restoring state unexpectedly")
		}
	}()

	sys := config.Get().System
	// Ensure the archive directory exists.
	if err := os.MkdirAll(sys.ArchiveDirectory, 0o755); err != nil {
		log.WithField("error", err).Error("failed to create archive directory")
	}

	// Ensure the backup directory exists.
	if err := os.MkdirAll(sys.BackupDirectory, 0o755); err != nil {
		log.WithField("error", err).Error("failed to create backup directory")
	}

	autotls, _ := cmd.Flags().GetBool("auto-tls")
	tlshostname, _ := cmd.Flags().GetString("tls-hostname")
	if autotls && tlshostname == "" {
		autotls = false
	}

	api := config.Get().Api
	log.WithFields(log.Fields{
		"use_ssl":      api.Ssl.Enabled,
		"use_auto_tls": autotls,
		"host_address": api.Host,
		"host_port":    api.Port,
	}).Info("configuring internal webserver")

	// Create a new HTTP server instance to handle inbound requests from the Panel
	// and external clients.
	s := &http.Server{
		Addr:      api.Host + ":" + strconv.Itoa(api.Port),
		Handler:   router.Configure(manager, pclient),
		TLSConfig: config.DefaultTLSConfig,
	}

	profile, _ := cmd.Flags().GetBool("pprof")
	if profile {
		if r, _ := cmd.Flags().GetInt("pprof-block-rate"); r > 0 {
			runtime.SetBlockProfileRate(r)
		}
		// Catch at least 1% of mutex contention issues.
		runtime.SetMutexProfileFraction(100)

		profilePort, _ := cmd.Flags().GetInt("pprof-port")
		go func() {
			http.ListenAndServe(fmt.Sprintf("localhost:%d", profilePort), nil)
		}()
	}

	// Check if the server should run with TLS but using autocert.
	if autotls {
		m := autocert.Manager{
			Prompt:     autocert.AcceptTOS,
			Cache:      autocert.DirCache(path.Join(sys.RootDirectory, "/.tls-cache")),
			HostPolicy: autocert.HostWhitelist(tlshostname),
		}

		log.WithField("hostname", tlshostname).Info("webserver is now listening with auto-TLS enabled; certificates will be automatically generated by Let's Encrypt")

		// Hook autocert into the main http server.
		s.TLSConfig.GetCertificate = m.GetCertificate
		s.TLSConfig.NextProtos = append(s.TLSConfig.NextProtos, acme.ALPNProto) // enable tls-alpn ACME challenges

		// Start the autocert server.
		go func() {
			if err := http.ListenAndServe(":http", m.HTTPHandler(nil)); err != nil {
				log.WithError(err).Error("failed to serve autocert http server")
			}
		}()
		// Start the main http server with TLS using autocert.
		if err := s.ListenAndServeTLS("", ""); err != nil {
			log.WithFields(log.Fields{"auto_tls": true, "tls_hostname": tlshostname, "error": err}).Fatal("failed to configure HTTP server using auto-tls")
		}
		return
	}

	// Check if main http server should run with TLS. Otherwise, reset the TLS
	// config on the server and then serve it over normal HTTP.
	if api.Ssl.Enabled {
		if err := s.ListenAndServeTLS(api.Ssl.CertificateFile, api.Ssl.KeyFile); err != nil {
			log.WithFields(log.Fields{"auto_tls": false, "error": err}).Fatal("failed to configure HTTPS server")
		}
		return
	}
	s.TLSConfig = nil
	if err := s.ListenAndServe(); err != nil {
		log.WithField("error", err).Fatal("failed to configure HTTP server")
	}
}

// Reads the configuration from the disk and then sets up the global singleton
// with all the configuration values.
func initConfig() {
	if !strings.HasPrefix(configPath, "/") {
		d, err := os.Getwd()
		if err != nil {
			log2.Fatalf("cmd/root: could not determine directory: %s", err)
		}
		configPath = path.Clean(path.Join(d, configPath))
	}
	err := config.FromFile(configPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			exitWithConfigurationNotice()
		}
		log2.Fatalf("cmd/root: error while reading configuration file: %s", err)
	}
	if debug && !config.Get().Debug {
		config.SetDebugViaFlag(debug)
	}
}

// Configures the global logger for Zap so that we can call it from any location
// in the code without having to pass around a logger instance.
func initLogging() {
	dir := config.Get().System.LogDirectory
	if err := os.MkdirAll(path.Join(dir, "/install"), 0o700); err != nil {
		log2.Fatalf("cmd/root: failed to create install directory path: %s", err)
	}
	p := filepath.Join(dir, "/wings.log")
	w, err := logrotate.NewFile(p)
	if err != nil {
		log2.Fatalf("cmd/root: failed to create wings log: %s", err)
	}
	log.SetLevel(log.InfoLevel)
	if config.Get().Debug {
		log.SetLevel(log.DebugLevel)
	}
	log.SetHandler(multi.New(cli.Default, cli.New(w.File, false)))
	log.WithField("path", p).Info("writing log files to disk")
}

// Prints the wings logo, nothing special here!
func printLogo() {
	fmt.Printf(colorstring.Color(`
                     ____
__ [blue][bold]Pterodactyl[reset] _____/___/_______ _______ ______
\_____\    \/\/    /   /       /  __   /   ___/
   \___\          /   /   /   /  /_/  /___   /
        \___/\___/___/___/___/___    /______/
                            /_______/ [bold]%s[reset]

Copyright © 2018 - %d Dane Everitt & Contributors

Website:  https://pterodactyl.io
 Source:  https://github.com/pterodactyl/wings
License:  https://github.com/pterodactyl/wings/blob/develop/LICENSE

This software is made available under the terms of the MIT license.
The above copyright notice and this permission notice shall be included
in all copies or substantial portions of the Software.%s`), system.Version, time.Now().Year(), "\n\n")
}

func exitWithConfigurationNotice() {
	fmt.Print(colorstring.Color(`
[_red_][white][bold]Error: Configuration File Not Found[reset]

Wings was not able to locate your configuration file, and therefore is not
able to complete its boot process. Please ensure you have copied your instance
configuration file into the default location below.

Default Location: /etc/pterodactyl/config.yml

[yellow]This is not a bug with this software. Please do not make a bug report
for this issue, it will be closed.[reset]

`))
	os.Exit(1)
}
