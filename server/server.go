package server

import (
	"fmt"
	"github.com/creasty/defaults"
	"github.com/patrickmn/go-cache"
	"github.com/pkg/errors"
	"github.com/pterodactyl/wings/api"
	"github.com/pterodactyl/wings/config"
	"github.com/remeh/sizedwaitgroup"
	"go.uber.org/zap"
	"gopkg.in/yaml.v2"
	"io/ioutil"
	"os"
	"path"
	"strings"
	"sync"
	"time"
)

var servers *Collection

func GetServers() *Collection {
	return servers
}

// High level definition for a server instance being controlled by Wings.
type Server struct {
	// The unique identifier for the server that should be used when referencing
	// it aganist the Panel API (and internally). This will be used when naming
	// docker containers as well as in log output.
	Uuid string `json:"uuid"`

	// Wether or not the server is in a suspended state. Suspended servers cannot
	// be started or modified except in certain scenarios by an admin user.
	Suspended bool `json:"suspended"`

	// The power state of the server.
	State string `default:"offline" json:"state"`

	// The command that should be used when booting up the server instance.
	Invocation string `json:"invocation"`

	// An array of environment variables that should be passed along to the running
	// server process.
	EnvVars map[string]string `json:"environment" yaml:"environment"`

	CrashDetection CrashDetection `json:"crash_detection" yaml:"crash_detection"`
	Build          BuildSettings  `json:"build"`
	Allocations    Allocations    `json:"allocations"`
	Environment    Environment    `json:"-" yaml:"-"`
	Filesystem     Filesystem     `json:"-" yaml:"-"`
	Resources      ResourceUsage  `json:"resources" yaml:"-"`

	Container struct {
		// Defines the Docker image that will be used for this server
		Image string `json:"image,omitempty"`
		// If set to true, OOM killer will be disabled on the server's Docker container.
		// If not present (nil) we will default to disabling it.
		OomDisabled bool `default:"true" json:"oom_disabled" yaml:"oom_disabled"`
	} `json:"container,omitempty"`

	// Server cache used to store frequently requested information in memory and make
	// certain long operations return faster. For example, FS disk space usage.
	Cache *cache.Cache `json:"-" yaml:"-"`

	// All of the registered event listeners for this server instance.
	listeners EventListeners

	// Defines the process configuration for the server instance. This is dynamically
	// fetched from the Pterodactyl Server instance each time the server process is
	// started, and then cached here.
	processConfiguration *api.ProcessConfiguration

	// Internal mutex used to block actions that need to occur sequentially, such as
	// writing the configuration to the disk.
	mutex *sync.Mutex
}

// The build settings for a given server that impact docker container creation and
// resource limits for a server instance.
type BuildSettings struct {
	// The total amount of memory in megabytes that this server is allowed to
	// use on the host system.
	MemoryLimit int64 `json:"memory_limit" yaml:"memory"`

	// The amount of additional swap space to be provided to a container instance.
	Swap int64 `json:"swap"`

	// The relative weight for IO operations in a container. This is relative to other
	// containers on the system and should be a value between 10 and 1000.
	IoWeight uint16 `json:"io_weight" yaml:"io"`

	// The percentage of CPU that this instance is allowed to consume relative to
	// the host. A value of 200% represents complete utilization of two cores. This
	// should be a value between 1 and THREAD_COUNT * 100.
	CpuLimit int64 `json:"cpu_limit" yaml:"cpu"`

	// The amount of disk space in megabytes that a server is allowed to use.
	DiskSpace int64 `json:"disk_space" yaml:"disk"`
}

// Converts the CPU limit for a server build into a number that can be better understood
// by the Docker environment. If there is no limit set, return -1 which will indicate to
// Docker that it has unlimited CPU quota.
func (b *BuildSettings) ConvertedCpuLimit() int64 {
	if b.CpuLimit == 0 {
		return -1
	}

	return b.CpuLimit * 1000
}

// Returns the amount of swap available as a total in bytes. This is returned as the amount
// of memory available to the server initially, PLUS the amount of additional swap to include
// which is the format used by Docker.
func (b *BuildSettings) ConvertedSwap() int64 {
	if b.Swap < 0 {
		return -1
	}

	return (b.Swap * 1000000) + (b.MemoryLimit * 1000000)
}

// Defines the allocations available for a given server. When using the Docker environment
// driver these correspond to mappings for the container that allow external connections.
type Allocations struct {
	// Defines the default allocation that should be used for this server. This is
	// what will be used for {SERVER_IP} and {SERVER_PORT} when modifying configuration
	// files or the startup arguments for a server.
	DefaultMapping struct {
		Ip   string `json:"ip"`
		Port int    `json:"port"`
	} `json:"default" yaml:"default"`

	// Mappings contains all of the ports that should be assigned to a given server
	// attached to the IP they correspond to.
	Mappings map[string][]int `json:"mappings"`
}

// Iterates over a given directory and loads all of the servers listed before returning
// them to the calling function.
func LoadDirectory(dir string, cfg *config.SystemConfiguration) error {
	// We could theoretically use a standard wait group here, however doing
	// that introduces the potential to crash the program due to too many
	// open files. This wouldn't happen on a small setup, but once the daemon is
	// handling many servers you run that risk.
	//
	// For now just process 10 files at a time, that should be plenty fast to
	// read and parse the YAML. We should probably make this configurable down
	// the road to help big instances scale better.
	wg := sizedwaitgroup.New(10)

	f, err := ioutil.ReadDir(dir)
	if err != nil {
		return err
	}

	servers = NewCollection(nil)

	for _, file := range f {
		if !strings.HasSuffix(file.Name(), ".yml") || file.IsDir() {
			continue
		}

		wg.Add()
		// For each of the YAML files we find, parse it and create a new server
		// configuration object that can then be returned to the caller.
		go func(file os.FileInfo) {
			defer wg.Done()

			b, err := ioutil.ReadFile(path.Join(dir, file.Name()))
			if err != nil {
				zap.S().Errorw("failed to read server configuration file, skipping...", zap.String("server", file.Name()), zap.Error(err))
				return
			}

			s, err := FromConfiguration(b, cfg)
			if err != nil {
				if IsServerDoesNotExistError(err) {
					zap.S().Infow("server does not exist on remote system", zap.String("server", file.Name()))
				} else {
					zap.S().Errorw("failed to parse server configuration, skipping...", zap.String("server", file.Name()), zap.Error(err))
				}

				return
			}

			servers.Add(s)
		}(file)
	}

	// Wait until we've processed all of the configuration files in the directory
	// before continuing.
	wg.Wait()

	return nil
}

// Initializes the default required internal struct components for a Server.
func (s *Server) Init() {
	s.listeners = make(map[string][]EventListenerFunction)
	s.mutex = &sync.Mutex{}
}

// Initalizes a server using a data byte array. This will be marshaled into the
// given struct using a YAML marshaler. This will also configure the given environment
// for a server.
func FromConfiguration(data []byte, cfg *config.SystemConfiguration) (*Server, error) {
	s := new(Server)

	if err := defaults.Set(s); err != nil {
		return nil, err
	}

	s.Init()

	if err := yaml.Unmarshal(data, s); err != nil {
		return nil, err
	}

	s.AddEventListeners()

	// Right now we only support a Docker based environment, so I'm going to hard code
	// this logic in. When we're ready to support other environment we'll need to make
	// some modifications here obviously.
	if err := NewDockerEnvironment(s); err != nil {
		return nil, err
	}

	s.Cache = cache.New(time.Minute*10, time.Minute*15)
	s.Filesystem = Filesystem{
		Configuration: cfg,
		Server:        s,
	}
	s.Resources = ResourceUsage{}

	// This is also done when the server is booted, however we need to account for instances
	// where the server is already running and the Daemon reboots. In those cases this will
	// allow us to you know, stop servers.
	if cfg.SyncServersOnBoot {
		if err := s.Sync(); err != nil {
			return nil, err
		}
	}

	return s, nil
}

// Syncs the state of the server on the Panel with Wings. This ensures that we're always
// using the state of the server from the Panel and allows us to not require successful
// API calls to Wings to do things.
//
// This also means mass actions can be performed against servers on the Panel and they
// will automatically sync with Wings when the server is started.
func (s *Server) Sync() error {
	cfg, rerr, err := s.GetProcessConfiguration()
	if err != nil || rerr != nil {
		if err != nil {
			return errors.WithStack(err)
		}

		if rerr.Status == "404" {
			return &serverDoesNotExist{}
		}

		return errors.New(rerr.String())
	}

	// Update the data structure and persist it to the disk.
	if err:= s.UpdateDataStructure(cfg.Settings, false); err != nil {
		return errors.WithStack(err)
	}

	s.processConfiguration = cfg.ProcessConfiguration

	return nil
}

// Reads the log file for a server up to a specified number of bytes.
func (s *Server) ReadLogfile(len int64) ([]string, error) {
	return s.Environment.Readlog(len)
}

// Determine if the server is bootable in it's current state or not. This will not
// indicate why a server is not bootable, only if it is.
func (s *Server) IsBootable() bool {
	exists, _ := s.Environment.Exists()

	return exists
}

// Initalizes a server instance. This will run through and ensure that the environment
// for the server is setup, and that all of the necessary files are created.
func (s *Server) CreateEnvironment() error {
	return s.Environment.Create()
}

const (
	ProcessOfflineState  = "offline"
	ProcessStartingState = "starting"
	ProcessRunningState  = "running"
	ProcessStoppingState = "stopping"
)

// Sets the state of the server internally. This function handles crash detection as
// well as reporting to event listeners for the server.
func (s *Server) SetState(state string) error {
	if state != ProcessOfflineState && state != ProcessStartingState && state != ProcessRunningState && state != ProcessStoppingState {
		return errors.New(fmt.Sprintf("invalid server state received: %s", state))
	}

	prevState := s.State
	s.State = state

	// Persist this change to the disk immediately so that should the Daemon be stopped or
	// crash we can immediately restore the server state.
	//
	// This really only makes a difference if all of the Docker containers are also stopped,
	// but this was a highly requested feature and isn't hard to work with, so lets do it.
	//
	// We also get the benefit of server status changes always propagating corrected configurations
	// to the disk should we forget to do it elsewhere.
	go func(server *Server) {
		if _, err := server.WriteConfigurationToDisk(); err != nil {
			zap.S().Warnw("failed to write server state change to disk", zap.String("server", server.Uuid), zap.Error(err))
		}
	}(s)

	zap.S().Debugw("saw server status change event", zap.String("server", s.Uuid), zap.String("status", s.State))

	// Emit the event to any listeners that are currently registered.
	s.Emit(StatusEvent, s.State)

	// If server was in an online state, and is now in an offline state we should handle
	// that as a crash event. In that scenario, check the last crash time, and the crash
	// counter.
	//
	// In the event that we have passed the thresholds, don't do anything, otherwise
	// automatically attempt to start the process back up for the user. This is done in a
	// seperate thread as to not block any actions currently taking place in the flow
	// that called this function.
	if (prevState == ProcessStartingState || prevState == ProcessRunningState) && s.State == ProcessOfflineState {
		zap.S().Infow("detected server as entering a potentially crashed state; running handler", zap.String("server", s.Uuid))

		go func(server *Server) {
			if err := server.handleServerCrash(); err != nil {
				if IsTooFrequentCrashError(err) {
					zap.S().Infow("did not restart server after crash; occurred too soon after last", zap.String("server", server.Uuid))
				} else {
					zap.S().Errorw("failed to handle server crash state", zap.String("server", server.Uuid), zap.Error(err))
				}
			}
		}(s)
	}

	return nil
}

// Gets the process configuration data for the server.
func (s *Server) GetProcessConfiguration() (*api.ServerConfigurationResponse, *api.RequestError, error) {
	return api.NewRequester().GetServerConfiguration(s.Uuid)
}
