package server

import (
	"github.com/apex/log"
	"github.com/creasty/defaults"
	"github.com/gammazero/workerpool"
	"github.com/patrickmn/go-cache"
	"github.com/pkg/errors"
	"github.com/pterodactyl/wings/api"
	"github.com/pterodactyl/wings/environment"
	"github.com/pterodactyl/wings/environment/docker"
	"os"
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

	configs, rerr, err := api.NewRequester().GetAllServerConfigurations()
	if err != nil || rerr != nil {
		if err != nil {
			return errors.WithStack(err)
		}

		return errors.New(rerr.String())
	}

	log.Debug("retrieving cached server states from disk")
	states, err := getServerStates()
	if err != nil {
		return errors.WithStack(err)
	}

	log.WithField("total_configs", len(configs)).Debug("looping over received configurations from API")

	pool := workerpool.New(runtime.NumCPU())
	for uuid, data := range configs {
		uuid := uuid
		data := data

		pool.Submit(func() {
			log.WithField("uuid", uuid).Debug("creating server object from configuration")
			s, err := FromConfiguration(data)
			if err != nil {
				log.WithField("server", uuid).WithField("error", err).Error("failed to load server, skipping...")
				return
			}

			if state, exists := states[s.Id()]; exists {
				s.SetState(state)
				s.Log().WithField("state", s.GetState()).Debug("loaded server state from cache file")
			}

			servers.Add(s)
		})
	}

	// Wait until we've processed all of the configuration files in the directory
	// before continuing.
	pool.StopWait()

	return nil
}

// Initializes a server using a data byte array. This will be marshaled into the
// given struct using a YAML marshaler. This will also configure the given environment
// for a server.
func FromConfiguration(data *api.ServerConfigurationResponse) (*Server, error) {
	cfg := Configuration{}
	if err := defaults.Set(&cfg); err != nil {
		return nil, err
	}

	s := new(Server)
	s.cfg = cfg

	if err := s.UpdateDataStructure(data.Settings, false); err != nil {
		return nil, err
	}

	s.cache = cache.New(time.Minute*10, time.Minute*15)
	s.Archiver = Archiver{Server: s}
	s.Filesystem = Filesystem{Server: s}

	// Right now we only support a Docker based environment, so I'm going to hard code
	// this logic in. When we're ready to support other environment we'll need to make
	// some modifications here obviously.
	envCfg := environment.NewConfiguration(s.Mounts(), s.cfg.Allocations, s.cfg.Build, s.cfg.EnvVars)
	meta := docker.Metadata{
		Invocation: s.Config().Invocation,
		Image:      s.Config().Container.Image,
	}

	if env, err := docker.New(s.Id(), &meta, envCfg); err != nil {
		return nil, err
	} else {
		s.Environment = env
		s.StartEventListeners()
	}

	// Forces the configuration to be synced with the panel.
	if err := s.SyncWithConfiguration(data); err != nil {
		return nil, err
	}

	// If the server's data directory exists, force disk usage calculation.
	if _, err := os.Stat(s.Filesystem.Path()); err == nil {
		go s.Filesystem.HasSpaceAvailable()
	}

	return s, nil
}
