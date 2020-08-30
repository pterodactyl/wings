package server

import (
	"fmt"
	"github.com/apex/log"
	"github.com/creasty/defaults"
	"github.com/gammazero/workerpool"
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

	log.Info("fetching list of servers from API")
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
		log.WithField("error", errors.WithStack(err)).Error("failed to retrieve locally cached server states from disk, assuming all servers in offline state")
	}

	start := time.Now()
	log.WithField("total_configs", len(configs)).Info("processing servers returned by the API")

	pool := workerpool.New(runtime.NumCPU())
	for uuid, data := range configs {
		uuid := uuid
		data := data

		pool.Submit(func() {
			log.WithField("server", uuid).Info("creating new server object from API response")
			s, err := FromConfiguration(data)
			if err != nil {
				log.WithField("server", uuid).WithField("error", err).Error("failed to load server, skipping...")
				return
			}

			if state, exists := states[s.Id()]; exists {
				s.Log().WithField("state", state).Debug("found existing server state in cache file; re-instantiating server state")
				s.SetState(state)
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
func FromConfiguration(data *api.ServerConfigurationResponse) (*Server, error) {
	cfg := Configuration{}
	if err := defaults.Set(&cfg); err != nil {
		return nil, errors.Wrap(err, "failed to set struct defaults for server configuration")
	}

	s := new(Server)
	if err := defaults.Set(s); err != nil {
		return nil, errors.Wrap(err, "failed to set struct defaults for server")
	}

	s.cfg = cfg
	if err := s.UpdateDataStructure(data.Settings); err != nil {
		return nil, err
	}

	s.Archiver = Archiver{Server: s}
	s.Filesystem = Filesystem{Server: s}

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
	}

	// Forces the configuration to be synced with the panel.
	if err := s.SyncWithConfiguration(data); err != nil {
		return nil, err
	}

	// If the server's data directory exists, force disk usage calculation.
	if _, err := os.Stat(s.Filesystem.Path()); err == nil {
		go s.Filesystem.HasSpaceAvailable(true)
	}

	return s, nil
}
