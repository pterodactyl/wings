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
	"math"
	"os"
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
	// it against the Panel API (and internally). This will be used when naming
	// docker containers as well as in log output.
	Uuid string `json:"uuid"`

	// Whether or not the server is in a suspended state. Suspended servers cannot
	// be started or modified except in certain scenarios by an admin user.
	Suspended bool `json:"suspended"`

	// The power state of the server.
	State string `default:"offline" json:"state"`

	// The command that should be used when booting up the server instance.
	Invocation string `json:"invocation"`

	// An array of environment variables that should be passed along to the running
	// server process.
	EnvVars map[string]string `json:"environment"`

	Allocations    Allocations    `json:"allocations"`
	Build          BuildSettings  `json:"build"`
	CrashDetection CrashDetection `json:"crash_detection"`
	Mounts         []Mount        `json:"mounts"`
	Resources      ResourceUsage  `json:"resources"`

	Archiver    Archiver    `json:"-"`
	Environment Environment `json:"-"`
	Filesystem  Filesystem  `json:"-"`

	Container struct {
		// Defines the Docker image that will be used for this server
		Image string `json:"image,omitempty"`
		// If set to true, OOM killer will be disabled on the server's Docker container.
		// If not present (nil) we will default to disabling it.
		OomDisabled bool `default:"true" json:"oom_disabled"`
	} `json:"container,omitempty"`

	// Server cache used to store frequently requested information in memory and make
	// certain long operations return faster. For example, FS disk space usage.
	Cache *cache.Cache `json:"-"`

	// Events emitted by the server instance.
	emitter *EventBus

	// Defines the process configuration for the server instance. This is dynamically
	// fetched from the Pterodactyl Server instance each time the server process is
	// started, and then cached here.
	processConfiguration *api.ProcessConfiguration

	// Internal mutex used to block actions that need to occur sequentially, such as
	// writing the configuration to the disk.
	sync.RWMutex
}

// The build settings for a given server that impact docker container creation and
// resource limits for a server instance.
type BuildSettings struct {
	// The total amount of memory in megabytes that this server is allowed to
	// use on the host system.
	MemoryLimit int64 `json:"memory_limit"`

	// The amount of additional swap space to be provided to a container instance.
	Swap int64 `json:"swap"`

	// The relative weight for IO operations in a container. This is relative to other
	// containers on the system and should be a value between 10 and 1000.
	IoWeight uint16 `json:"io_weight"`

	// The percentage of CPU that this instance is allowed to consume relative to
	// the host. A value of 200% represents complete utilization of two cores. This
	// should be a value between 1 and THREAD_COUNT * 100.
	CpuLimit int64 `json:"cpu_limit"`

	// The amount of disk space in megabytes that a server is allowed to use.
	DiskSpace int64 `json:"disk_space"`

	// Sets which CPU threads can be used by the docker instance.
	Threads string `json:"threads"`
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

// Set the hard limit for memory usage to be 5% more than the amount of memory assigned to
// the server. If the memory limit for the server is < 4G, use 10%, if less than 2G use
// 15%. This avoids unexpected crashes from processes like Java which run over the limit.
func (b *BuildSettings) MemoryOverheadMultiplier() float64 {
	if b.MemoryLimit <= 2048 {
		return 1.15
	} else if b.MemoryLimit <= 4096 {
		return 1.10
	}

	return 1.05
}

func (b *BuildSettings) BoundedMemoryLimit() int64 {
	return int64(math.Round(float64(b.MemoryLimit) * b.MemoryOverheadMultiplier() * 1_000_000))
}

// Returns the amount of swap available as a total in bytes. This is returned as the amount
// of memory available to the server initially, PLUS the amount of additional swap to include
// which is the format used by Docker.
func (b *BuildSettings) ConvertedSwap() int64 {
	if b.Swap < 0 {
		return -1
	}

	return (b.Swap * 1_000_000) + b.BoundedMemoryLimit()
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
	} `json:"default"`

	// Mappings contains all of the ports that should be assigned to a given server
	// attached to the IP they correspond to.
	Mappings map[string][]int `json:"mappings"`
}

// Iterates over a given directory and loads all of the servers listed before returning
// them to the calling function.
func LoadDirectory() error {
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

	states, err := getServerStates()
	if err != nil {
		return errors.WithStack(err)
	}

	servers = NewCollection(nil)

	for uuid, data := range configs {
		wg.Add()

		go func(uuid string, data *api.ServerConfigurationResponse) {
			defer wg.Done()

			s, err := FromConfiguration(data)
			if err != nil {
				zap.S().Errorw("failed to load server, skipping...", zap.String("server", uuid), zap.Error(err))
				return
			}

			if state, exists := states[s.Uuid]; exists {
				s.SetState(state)
				zap.S().Debugw("loaded server state from cache", zap.String("server", s.Uuid), zap.String("state", s.GetState()))
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
func FromConfiguration(data *api.ServerConfigurationResponse) (*Server, error) {
	s := new(Server)

	if err := defaults.Set(s); err != nil {
		return nil, err
	}

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

	s.Cache = cache.New(time.Minute*10, time.Minute*15)
	s.Archiver = Archiver{
		Server: s,
	}
	s.Filesystem = Filesystem{
		Configuration: &config.Get().System,
		Server:        s,
	}
	s.Resources = ResourceUsage{}

	// Forces the configuration to be synced with the panel.
	if err := s.SyncWithConfiguration(data); err != nil {
		return nil, err
	}

	return s, nil
}

// Returns all of the environment variables that should be assigned to a running
// server instance.
func (s *Server) GetEnvironmentVariables() []string {
	zone, _ := time.Now().In(time.Local).Zone()

	var out = []string{
		fmt.Sprintf("TZ=%s", zone),
		fmt.Sprintf("STARTUP=%s", s.Invocation),
		fmt.Sprintf("SERVER_MEMORY=%d", s.Build.MemoryLimit),
		fmt.Sprintf("SERVER_IP=%s", s.Allocations.DefaultMapping.Ip),
		fmt.Sprintf("SERVER_PORT=%d", s.Allocations.DefaultMapping.Port),
	}

eloop:
	for k, v := range s.EnvVars {
		for _, e := range out {
			if strings.HasPrefix(e, strings.ToUpper(k)) {
				continue eloop
			}
		}

		out = append(out, fmt.Sprintf("%s=%s", strings.ToUpper(k), v))
	}

	return out
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

	return s.SyncWithConfiguration(cfg)
}

func (s *Server) SyncWithConfiguration(cfg *api.ServerConfigurationResponse) error {
	// Update the data structure and persist it to the disk.
	if err := s.UpdateDataStructure(cfg.Settings, false); err != nil {
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

// Gets the process configuration data for the server.
func (s *Server) GetProcessConfiguration() (*api.ServerConfigurationResponse, *api.RequestError, error) {
	return api.NewRequester().GetServerConfiguration(s.Uuid)
}

// Helper function that can receieve a power action and then process the
// actions that need to occur for it.
func (s *Server) HandlePowerAction(action PowerAction) error {
	switch action.Action {
	case "start":
		return s.Environment.Start()
	case "restart":
		if err := s.Environment.WaitForStop(60, false); err != nil {
			return err
		}

		return s.Environment.Start()
	case "stop":
		return s.Environment.Stop()
	case "kill":
		return s.Environment.Terminate(os.Kill)
	default:
		return errors.New("an invalid power action was provided")
	}
}
