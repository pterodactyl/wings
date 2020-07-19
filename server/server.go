package server

import (
	"context"
	"fmt"
	"github.com/apex/log"
	"github.com/patrickmn/go-cache"
	"github.com/pkg/errors"
	"github.com/pterodactyl/wings/api"
	"golang.org/x/sync/semaphore"
	"os"
	"strings"
	"sync"
	"time"
)

// High level definition for a server instance being controlled by Wings.
type Server struct {
	// Internal mutex used to block actions that need to occur sequentially, such as
	// writing the configuration to the disk.
	sync.RWMutex

	// The unique identifier for the server that should be used when referencing
	// it against the Panel API (and internally). This will be used when naming
	// docker containers as well as in log output.
	Uuid string `json:"-"`

	// Maintains the configuration for the server. This is the data that gets returned by the Panel
	// such as build settings and container images.
	cfg Configuration

	// The crash handler for this server instance.
	crasher CrashHandler

	resources   ResourceUsage
	Archiver    Archiver    `json:"-"`
	Environment Environment `json:"-"`
	Filesystem  Filesystem  `json:"-"`

	// Server cache used to store frequently requested information in memory and make
	// certain long operations return faster. For example, FS disk space usage.
	cache *cache.Cache

	// Events emitted by the server instance.
	emitter *EventBus

	// Defines the process configuration for the server instance. This is dynamically
	// fetched from the Pterodactyl Server instance each time the server process is
	// started, and then cached here.
	procConfig *api.ProcessConfiguration

	// Tracks the installation process for this server and prevents a server from running
	// two installer processes at the same time. This also allows us to cancel a running
	// installation process, for example when a server is deleted from the panel while the
	// installer process is still running.
	installer InstallerDetails
}

type InstallerDetails struct {
	// The cancel function for the installer. This will be a non-nil value while there
	// is an installer running for the server.
	cancel *context.CancelFunc

	// Installer lock. You should obtain an exclusive lock on this context while running
	// the installation process and release it when finished.
	sem *semaphore.Weighted
}

// Returns the UUID for the server instance.
func (s *Server) Id() string {
	return s.Config().Uuid
}

// Returns all of the environment variables that should be assigned to a running
// server instance.
func (s *Server) GetEnvironmentVariables() []string {
	zone, _ := time.Now().In(time.Local).Zone()

	var out = []string{
		fmt.Sprintf("TZ=%s", zone),
		fmt.Sprintf("STARTUP=%s", s.Config().Invocation),
		fmt.Sprintf("SERVER_MEMORY=%d", s.Build().MemoryLimit),
		fmt.Sprintf("SERVER_IP=%s", s.Config().Allocations.DefaultMapping.Ip),
		fmt.Sprintf("SERVER_PORT=%d", s.Config().Allocations.DefaultMapping.Port),
	}

eloop:
	for k := range s.Config().EnvVars {
		for _, e := range out {
			if strings.HasPrefix(e, strings.ToUpper(k)) {
				continue eloop
			}
		}

		out = append(out, fmt.Sprintf("%s=%s", strings.ToUpper(k), s.Config().EnvVars.Get(k)))
	}

	return out
}

func (s *Server) Log() *log.Entry {
	return log.WithField("server", s.Uuid)
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

	s.Lock()
	s.procConfig = cfg.ProcessConfiguration
	s.Unlock()

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
		return s.Environment.Restart()
	case "stop":
		return s.Environment.Stop()
	case "kill":
		return s.Environment.Terminate(os.Kill)
	default:
		return errors.New("an invalid power action was provided")
	}
}

// Checks if the server is marked as being suspended or not on the system.
func (s *Server) IsSuspended() bool {
	return s.Config().Suspended
}
