package cmd

import (
	"crypto/tls"
	"fmt"
	"github.com/gammazero/workerpool"
	"net/http"
	"os"
	"path"
	"strings"

	"github.com/apex/log"
	"github.com/mitchellh/colorstring"
	"github.com/pterodactyl/wings/loggers/cli"
	"golang.org/x/crypto/acme/autocert"

	"github.com/pkg/errors"
	"github.com/pkg/profile"
	"github.com/pterodactyl/wings/config"
	"github.com/pterodactyl/wings/environment"
	"github.com/pterodactyl/wings/router"
	"github.com/pterodactyl/wings/server"
	"github.com/pterodactyl/wings/sftp"
	"github.com/pterodactyl/wings/system"
	"github.com/spf13/cobra"
	"go.uber.org/zap"
)

var configPath = config.DefaultLocation
var debug = false
var shouldRunProfiler = false
var useAutomaticTls = false
var tlsHostname = ""
var showVersion = false

var root = &cobra.Command{
	Use:   "wings",
	Short: "The wings of the pterodactyl game management panel",
	Long:  ``,
	PreRun: func(cmd *cobra.Command, args []string) {
		if useAutomaticTls && len(tlsHostname) == 0 {
			fmt.Println("A TLS hostname must be provided when running wings with automatic TLS, e.g.:\n\n    ./wings --auto-tls --tls-hostname my.example.com")
			os.Exit(1)
		}
	},
	Run: rootCmdRun,
}

func init() {
	root.PersistentFlags().BoolVar(&showVersion, "version", false, "show the version and exit")
	root.PersistentFlags().StringVar(&configPath, "config", config.DefaultLocation, "set the location for the configuration file")
	root.PersistentFlags().BoolVar(&debug, "debug", false, "pass in order to run wings in debug mode")
	root.PersistentFlags().BoolVar(&shouldRunProfiler, "profile", false, "pass in order to profile wings")
	root.PersistentFlags().BoolVar(&useAutomaticTls, "auto-tls", false, "pass in order to have wings generate and manage it's own SSL certificates using Let's Encrypt")
	root.PersistentFlags().StringVar(&tlsHostname, "tls-hostname", "", "required with --auto-tls, the FQDN for the generated SSL certificate")

	root.AddCommand(configureCmd)
	root.AddCommand(diagnosticsCmd)
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
	if showVersion {
		fmt.Println(system.Version)
		os.Exit(0)
	}

	if shouldRunProfiler {
		defer profile.Start().Stop()
	}

	// Only attempt configuration file relocation if a custom location has not
	// been specified in the command startup.
	if configPath == config.DefaultLocation {
		if err := RelocateConfiguration(); err != nil {
			if os.IsNotExist(err) {
				exitWithConfigurationNotice()
			}

			panic(err)
		}
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

	log.WithField("path", c.GetPath()).Info("loading configuration from path")
	if c.Debug {
		log.Debug("running in debug mode")
		log.Info("certificate checking is disabled")

		http.DefaultTransport.(*http.Transport).TLSClientConfig = &tls.Config{
			InsecureSkipVerify: true,
		}
	}

	config.Set(c)
	config.SetDebugViaFlag(debug)

	if err := c.System.ConfigureDirectories(); err != nil {
		log.Fatal("failed to configure system directories for pterodactyl")
		panic(err)
	}

	log.WithField("username", c.System.Username).Info("checking for pterodactyl system user")
	if su, err := c.EnsurePterodactylUser(); err != nil {
		log.Error("failed to create pterodactyl system user")
		panic(err)
		return
	} else {
		log.WithFields(log.Fields{
			"username": su.Username,
			"uid":      su.Uid,
			"gid":      su.Gid,
		}).Info("configured system user successfully")
	}

	if err := server.LoadDirectory(); err != nil {
		log.WithField("error", err).Fatal("failed to load server configurations")
		return
	}

	if err := environment.ConfigureDocker(&c.Docker); err != nil {
		log.WithField("error", err).Fatal("failed to configure docker environment")
		os.Exit(1)
	}

	if err := c.WriteToDisk(); err != nil {
		log.WithField("error", err).Error("failed to save configuration to disk")
	}

	// Just for some nice log output.
	for _, s := range server.GetServers().All() {
		log.WithField("server", s.Id()).Info("loaded configuration for server")
	}

	if !c.System.SetPermissionsOnBoot {
		log.Warn("server file permission checking is currently disabled on boot!")
	}

	// Create a new workerpool that limits us to 4 servers being bootstrapped at a time
	// on Wings. This allows us to ensure the environment exists, write configurations,
	// and reboot processes without causing a slow-down due to sequential booting.
	pool := workerpool.New(4)

	for _, serv := range server.GetServers().All() {
		s := serv

		pool.Submit(func() {
			if c.System.SetPermissionsOnBoot {
				s.Log().Info("chowning server data directory")
				if err := s.Filesystem.Chown("/"); err != nil {
					s.Log().WithField("error", err).Warn("error during server data directory chown")
				}
			}

			s.Log().Info("ensuring server environment exists")
			// Create a server environment if none exists currently. This allows us to recover from Docker
			// being reinstalled on the host system for example.
			if err := s.Environment.Create(); err != nil {
				s.Log().WithField("error", err).Error("failed to process environment")
			}

			r, err := s.Environment.IsRunning()
			if err != nil {
				s.Log().WithField("error", err).Error("error checking server environment status")
			}

			// If the server is currently running on Docker, mark the process as being in that state.
			// We never want to stop an instance that is currently running external from Wings since
			// that is a good way of keeping things running even if Wings gets in a very corrupted state.
			//
			// This will also validate that a server process is running if the last tracked state we have
			// is that it was running, but we see that the container process is not currently running.
			if r || (!r && s.IsRunning()) {
				s.Log().Info("detected server is running, re-attaching to process...")
				if err := s.HandlePowerAction(server.PowerActionStart); err != nil {
					s.Log().WithField("error", errors.WithStack(err)).Warn("failed to properly start server detected as already running")
				}

				return
			}

			// Addresses potentially invalid data in the stored file that can cause Wings to lose
			// track of what the actual server state is.
			s.SetState(server.ProcessOfflineState)
		})
	}

	// Wait until all of the servers are ready to go before we fire up the HTTP server.
	pool.StopWait()

	// Initalize SFTP.
	sftp.Initialize(c)

	// Ensure the archive directory exists.
	if err := os.MkdirAll(c.System.ArchiveDirectory, 0755); err != nil {
		log.WithField("error", err).Error("failed to create archive directory")
	}

	// Ensure the backup directory exists.
	if err := os.MkdirAll(c.System.BackupDirectory, 0755); err != nil {
		log.WithField("error", err).Error("failed to create backup directory")
	}

	log.WithFields(log.Fields{
		"use_ssl":      c.Api.Ssl.Enabled,
		"use_auto_tls": useAutomaticTls && len(tlsHostname) > 0,
		"host_address": c.Api.Host,
		"host_port":    c.Api.Port,
	}).Info("configuring internal webserver")

	r := router.Configure()
	addr := fmt.Sprintf("%s:%d", c.Api.Host, c.Api.Port)

	if useAutomaticTls && len(tlsHostname) > 0 {
		m := autocert.Manager{
			Prompt:     autocert.AcceptTOS,
			Cache:      autocert.DirCache(path.Join(c.System.RootDirectory, "/.tls-cache")),
			HostPolicy: autocert.HostWhitelist(tlsHostname),
		}

		log.WithField("hostname", tlsHostname).
			Info("webserver is now listening with auto-TLS enabled; certifcates will be automatically generated by Let's Encrypt")

		// We don't use the autotls runner here since we need to specify a port other than 443
		// to be using for SSL connections for Wings.
		s := &http.Server{Addr: addr, TLSConfig: m.TLSConfig(), Handler: r}

		go http.ListenAndServe(":http", m.HTTPHandler(nil))
		if err := s.ListenAndServeTLS("", ""); err != nil {
			log.WithFields(log.Fields{"auto_tls": true, "tls_hostname": tlsHostname, "error": err}).
				Fatal("failed to configure HTTP server using auto-tls")
			os.Exit(1)
		}
	} else if c.Api.Ssl.Enabled {
		if err := r.RunTLS(addr, c.Api.Ssl.CertificateFile, c.Api.Ssl.KeyFile); err != nil {
			log.WithFields(log.Fields{"auto_tls": false, "error": err}).Fatal("failed to configure HTTPS server")
			os.Exit(1)
		}
	} else {
		if err := r.Run(addr); err != nil {
			log.WithField("error", err).Fatal("failed to configure HTTP server")
			os.Exit(1)
		}
	}
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

	log.SetHandler(cli.Default)
	log.SetLevel(log.DebugLevel)

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

func exitWithConfigurationNotice() {
	fmt.Print(colorstring.Color(`
[_red_][white][bold]Error: Configuration File Not Found[reset]

Wings was not able to locate your configuration file, and therefore is not
able to complete its boot process.

Please ensure you have copied your instance configuration file into
the default location, or have provided the --config flag to use a
custom location.

Default Location: /etc/pterodactyl/config.yml

[yellow]This is not a bug with this software. Please do not make a bug report
for this issue, it will be closed.[reset]

`))
	os.Exit(1)
}
