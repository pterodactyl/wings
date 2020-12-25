package server

import (
	"emperror.dev/errors"
	"encoding/json"
	"fmt"
	"github.com/apex/log"
	"github.com/gammazero/workerpool"
	"github.com/pterodactyl/wings/api"
	"github.com/pterodactyl/wings/config"
	"github.com/pterodactyl/wings/environment"
	"github.com/pterodactyl/wings/environment/docker"
	"github.com/pterodactyl/wings/server/filesystem"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

var servers = NewCollection(nil)

func GetServers() *Collection {
	return servers
}

// Iterates over a given directory and loads all of the servers listed before returning
// them to the calling function.
func LoadDirectory() error {
	if len(servers.items) != 0 {
		return errors.New("cannot call LoadDirectory with a non-nil collection")
	}

	log.Info("fetching list of servers from API")
	configs, err := api.New().GetServers()
	if err != nil {
		if !api.IsRequestError(err) {
			return err
		}

		return errors.New(err.Error())
	}

	start := time.Now()
	log.WithField("total_configs", len(configs)).Info("processing servers returned by the API")

	pool := workerpool.New(runtime.NumCPU())
	log.Debugf("using %d workerpools to instantiate server instances", runtime.NumCPU())
	for _, data := range configs {
		data := data

		pool.Submit(func() {
			// Parse the json.RawMessage into an expected struct value. We do this here so that a single broken
			// server does not cause the entire boot process to hang, and allows us to show more useful error
			// messaging in the output.
			d := api.ServerConfigurationResponse{
				Settings: data.Settings,
			}

			log.WithField("server", data.Uuid).Info("creating new server object from API response")
			if err := json.Unmarshal(data.ProcessConfiguration, &d.ProcessConfiguration); err != nil {
				log.WithField("server", data.Uuid).WithField("error", err).Error("failed to parse server configuration from API response, skipping...")
				return
			}

			s, err := FromConfiguration(d)
			if err != nil {
				log.WithField("server", data.Uuid).WithField("error", err).Error("failed to load server, skipping...")
				return
			}

			servers.Add(s)
		})
	}

	// Wait until we've processed all of the configuration files in the directory
	// before continuing.
	pool.StopWait()

	diff := time.Now().Sub(start)
	log.WithField("duration", fmt.Sprintf("%s", diff)).Info("finished processing server configurations")

	return nil
}

// Initializes a server using a data byte array. This will be marshaled into the
// given struct using a YAML marshaler. This will also configure the given environment
// for a server.
func FromConfiguration(data api.ServerConfigurationResponse) (*Server, error) {
	s, err := New()
	if err != nil {
		return nil, errors.WithMessage(err, "loader: failed to instantiate empty server struct")
	}
	if err := s.UpdateDataStructure(data.Settings); err != nil {
		return nil, err
	}

	s.Archiver = Archiver{Server: s}
	s.fs = filesystem.New(filepath.Join(config.Get().System.Data, s.Id()), s.DiskSpace())

	// Right now we only support a Docker based environment, so I'm going to hard code
	// this logic in. When we're ready to support other environment we'll need to make
	// some modifications here obviously.
	settings := environment.Settings{
		Mounts:      s.Mounts(),
		Allocations: s.cfg.Allocations,
		Limits:      s.cfg.Build,
	}

	envCfg := environment.NewConfiguration(settings, s.GetEnvironmentVariables())
	meta := docker.Metadata{
		Image: s.Config().Container.Image,
	}

	if env, err := docker.New(s.Id(), &meta, envCfg); err != nil {
		return nil, err
	} else {
		s.Environment = env
		s.StartEventListeners()
		s.Throttler().StartTimer(s.Context())
	}

	// Forces the configuration to be synced with the panel.
	if err := s.SyncWithConfiguration(data); err != nil {
		return nil, err
	}

	// If the server's data directory exists, force disk usage calculation.
	if _, err := os.Stat(s.Filesystem().Path()); err == nil {
		s.Filesystem().HasSpaceAvailable(true)
	}

	return s, nil
}
