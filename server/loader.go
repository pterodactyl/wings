package server

import (
	"github.com/apex/log"
	"github.com/creasty/defaults"
	"github.com/patrickmn/go-cache"
	"github.com/pkg/errors"
	"github.com/pterodactyl/wings/api"
	"github.com/pterodactyl/wings/config"
	"github.com/remeh/sizedwaitgroup"
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

	// We could theoretically use a standard wait group here, however doing
	// that introduces the potential to crash the program due to too many
	// open files. This wouldn't happen on a small setup, but once the daemon is
	// handling many servers you run that risk.
	//
	// For now just process 10 files at a time, that should be plenty fast to
	// read and parse the YAML. We should probably make this configurable down
	// the road to help big instances scale better.
	wg := sizedwaitgroup.New(10)

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
	for uuid, data := range configs {
		wg.Add()

		go func(uuid string, data *api.ServerConfigurationResponse) {
			defer wg.Done()

			log.WithField("uuid", uuid).Debug("creating server object from configuration")
			s, err := FromConfiguration(data, false)
			if err != nil {
				log.WithField("server", uuid).WithField("error", err).Error("failed to load server, skipping...")
				return
			}

			if state, exists := states[s.Id()]; exists {
				s.SetState(state)
				s.Log().WithField("state", s.GetState()).Debug("loaded server state from cache file")
			}

			servers.Add(s)
		}(uuid, data)
	}

	// Wait until we've processed all of the configuration files in the directory
	// before continuing.
	wg.Wait()

	return nil
}

// Initializes a server using a data byte array. This will be marshaled into the
// given struct using a YAML marshaler. This will also configure the given environment
// for a server.
func FromConfiguration(data *api.ServerConfigurationResponse, sync bool) (*Server, error) {
	cfg := Configuration{}
	if err := defaults.Set(&cfg); err != nil {
		return nil, err
	}

	s := new(Server)
	s.cfg = cfg

	if err := s.UpdateDataStructure(data.Settings, false); err != nil {
		return nil, err
	}

	s.AddEventListeners()

	// Right now we only support a Docker based environment, so I'm going to hard code
	// this logic in. When we're ready to support other environment we'll need to make
	// some modifications here obviously.
	if err := NewDockerEnvironment(s); err != nil {
		return nil, err
	}

	s.cache = cache.New(time.Minute*10, time.Minute*15)
	s.Archiver = Archiver{
		Server: s,
	}
	s.Filesystem = Filesystem{
		Configuration: &config.Get().System,
		Server:        s,
	}

	// Forces the configuration to be synced with the panel.
	if sync {
		if err := s.SyncWithConfiguration(data); err != nil {
			return nil, err
		}
	}

	return s, nil
}
