package server

import (
	"context"
	"emperror.dev/errors"
	"fmt"
	"github.com/apex/log"
	"github.com/creasty/defaults"
	"github.com/pterodactyl/wings/api"
	"github.com/pterodactyl/wings/config"
	"github.com/pterodactyl/wings/environment"
	"github.com/pterodactyl/wings/environment/docker"
	"github.com/pterodactyl/wings/events"
	"github.com/pterodactyl/wings/server/filesystem"
	"github.com/pterodactyl/wings/system"
	"golang.org/x/sync/semaphore"
	"strings"
	"sync"
)

// High level definition for a server instance being controlled by Wings.
type Server struct {
	// Internal mutex used to block actions that need to occur sequentially, such as
	// writing the configuration to the disk.
	sync.RWMutex
	ctx       context.Context
	ctxCancel *context.CancelFunc

	emitterLock  sync.Mutex
	powerLock    *semaphore.Weighted
	throttleOnce sync.Once

	// Maintains the configuration for the server. This is the data that gets returned by the Panel
	// such as build settings and container images.
	cfg Configuration

	// The crash handler for this server instance.
	crasher CrashHandler

	resources   ResourceUsage
	Archiver    Archiver                       `json:"-"`
	Environment environment.ProcessEnvironment `json:"-"`

	fs *filesystem.Filesystem

	// Events emitted by the server instance.
	emitter *events.EventBus

	// Defines the process configuration for the server instance. This is dynamically
	// fetched from the Pterodactyl Server instance each time the server process is
	// started, and then cached here.
	procConfig *api.ProcessConfiguration

	// Tracks the installation process for this server and prevents a server from running
	// two installer processes at the same time. This also allows us to cancel a running
	// installation process, for example when a server is deleted from the panel while the
	// installer process is still running.
	installing   *system.AtomicBool
	transferring *system.AtomicBool

	// The console throttler instance used to control outputs.
	throttler *ConsoleThrottler

	// Tracks open websocket connections for the server.
	wsBag       *WebsocketBag
	wsBagLocker sync.Mutex
}

// Returns a new server instance with a context and all of the default values set on
// the instance.
func New() (*Server, error) {
	ctx, cancel := context.WithCancel(context.Background())
	s := Server{
		ctx:          ctx,
		ctxCancel:    &cancel,
		installing:   system.NewAtomicBool(false),
		transferring: system.NewAtomicBool(false),
	}
	if err := defaults.Set(&s); err != nil {
		return nil, err
	}
	if err := defaults.Set(&s.cfg); err != nil {
		return nil, err
	}
	s.resources.State = system.NewAtomicString(environment.ProcessOfflineState)
	return &s, nil
}

// Returns the UUID for the server instance.
func (s *Server) Id() string {
	return s.Config().GetUuid()
}

// Cancels the context assigned to this server instance. Assuming background tasks
// are using this server's context for things, all of the background tasks will be
// stopped as a result.
func (s *Server) CtxCancel() {
	if s.ctxCancel != nil {
		(*s.ctxCancel)()
	}
}

// Returns a context instance for the server. This should be used to allow background
// tasks to be canceled if the server is removed. It will only be canceled when the
// application is stopped or if the server gets deleted.
func (s *Server) Context() context.Context {
	return s.ctx
}

// Returns all of the environment variables that should be assigned to a running
// server instance.
func (s *Server) GetEnvironmentVariables() []string {
	out := []string{
		fmt.Sprintf("TZ=%s", config.Get().System.Timezone),
		fmt.Sprintf("STARTUP=%s", s.Config().Invocation),
		fmt.Sprintf("SERVER_MEMORY=%d", s.MemoryLimit()),
		fmt.Sprintf("SERVER_IP=%s", s.Config().Allocations.DefaultMapping.Ip),
		fmt.Sprintf("SERVER_PORT=%d", s.Config().Allocations.DefaultMapping.Port),
	}

eloop:
	for k := range s.Config().EnvVars {
		// Don't allow any environment variables that we have already set above.
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
	return log.WithField("server", s.Id())
}

// Syncs the state of the server on the Panel with Wings. This ensures that we're always
// using the state of the server from the Panel and allows us to not require successful
// API calls to Wings to do things.
//
// This also means mass actions can be performed against servers on the Panel and they
// will automatically sync with Wings when the server is started.
func (s *Server) Sync() error {
	cfg, err := api.New().GetServerConfiguration(s.Id())
	if err != nil {
		if !api.IsRequestError(err) {
			return err
		}

		if err.(*api.RequestError).Status == "404" {
			return &serverDoesNotExist{}
		}

		return errors.New(err.Error())
	}

	return s.SyncWithConfiguration(cfg)
}

func (s *Server) SyncWithConfiguration(cfg api.ServerConfigurationResponse) error {
	// Update the data structure and persist it to the disk.
	if err := s.UpdateDataStructure(cfg.Settings); err != nil {
		return err
	}

	s.Lock()
	s.procConfig = cfg.ProcessConfiguration
	s.Unlock()

	// Update the disk space limits for the server whenever the configuration
	// for it changes.
	s.fs.SetDiskLimit(s.DiskSpace())

	// If this is a Docker environment we need to sync the stop configuration with it so that
	// the process isn't just terminated when a user requests it be stopped.
	if e, ok := s.Environment.(*docker.Environment); ok {
		s.Log().Debug("syncing stop configuration with configured docker environment")
		e.SetImage(s.Config().Container.Image)
		e.SetStopConfiguration(cfg.ProcessConfiguration.Stop)
	}

	return nil
}

// Reads the log file for a server up to a specified number of bytes.
func (s *Server) ReadLogfile(len int) ([]string, error) {
	return s.Environment.Readlog(len)
}

// Determine if the server is bootable in it's current state or not. This will not
// indicate why a server is not bootable, only if it is.
func (s *Server) IsBootable() bool {
	exists, _ := s.Environment.Exists()

	return exists
}

// Initializes a server instance. This will run through and ensure that the environment
// for the server is setup, and that all of the necessary files are created.
func (s *Server) CreateEnvironment() error {
	// Ensure the data directory exists before getting too far through this process.
	if err := s.EnsureDataDirectoryExists(); err != nil {
		return err
	}

	return s.Environment.Create()
}

// Checks if the server is marked as being suspended or not on the system.
func (s *Server) IsSuspended() bool {
	return s.Config().Suspended
}

func (s *Server) ProcessConfiguration() *api.ProcessConfiguration {
	s.RLock()
	defer s.RUnlock()

	return s.procConfig
}
