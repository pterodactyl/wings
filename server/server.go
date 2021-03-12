package server

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"

	"emperror.dev/errors"
	"github.com/apex/log"
	"github.com/creasty/defaults"
	"github.com/pterodactyl/wings/config"
	"github.com/pterodactyl/wings/environment"
	"github.com/pterodactyl/wings/environment/docker"
	"github.com/pterodactyl/wings/events"
	"github.com/pterodactyl/wings/remote"
	"github.com/pterodactyl/wings/server/filesystem"
	"github.com/pterodactyl/wings/system"
	"golang.org/x/sync/semaphore"
)

// Server is the high level definition for a server instance being controlled
// by Wings.
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
	cfg    Configuration
	client remote.Client

	// The crash handler for this server instance.
	crasher CrashHandler

	resources   ResourceUsage
	Environment environment.ProcessEnvironment `json:"-"`

	fs *filesystem.Filesystem

	// Events emitted by the server instance.
	emitter *events.EventBus

	// Defines the process configuration for the server instance. This is dynamically
	// fetched from the Pterodactyl Server instance each time the server process is
	// started, and then cached here.
	procConfig *remote.ProcessConfiguration

	// Tracks the installation process for this server and prevents a server from running
	// two installer processes at the same time. This also allows us to cancel a running
	// installation process, for example when a server is deleted from the panel while the
	// installer process is still running.
	installing   *system.AtomicBool
	transferring *system.AtomicBool
	restoring    *system.AtomicBool

	// The console throttler instance used to control outputs.
	throttler *ConsoleThrottler

	// Tracks open websocket connections for the server.
	wsBag       *WebsocketBag
	wsBagLocker sync.Mutex
}

// New returns a new server instance with a context and all of the default
// values set on the struct.
func New(client remote.Client) (*Server, error) {
	ctx, cancel := context.WithCancel(context.Background())
	s := Server{
		ctx:          ctx,
		ctxCancel:    &cancel,
		client:       client,
		installing:   system.NewAtomicBool(false),
		transferring: system.NewAtomicBool(false),
		restoring:    system.NewAtomicBool(false),
	}
	if err := defaults.Set(&s); err != nil {
		return nil, errors.Wrap(err, "server: could not set default values for struct")
	}
	if err := defaults.Set(&s.cfg); err != nil {
		return nil, errors.Wrap(err, "server: could not set defaults for server configuration")
	}
	s.resources.State = system.NewAtomicString(environment.ProcessOfflineState)
	return &s, nil
}

// Id returns the UUID for the server instance.
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
	cfg, err := s.client.GetServerConfiguration(s.Context(), s.Id())
	if err != nil {
		if !remote.IsRequestError(err) {
			return err
		}

		if err.(*remote.RequestError).Status == "404" {
			return &serverDoesNotExist{}
		}

		return errors.New(err.Error())
	}

	return s.SyncWithConfiguration(cfg)
}

func (s *Server) SyncWithConfiguration(cfg remote.ServerConfigurationResponse) error {
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

func (s *Server) ProcessConfiguration() *remote.ProcessConfiguration {
	s.RLock()
	defer s.RUnlock()

	return s.procConfig
}

// Filesystem returns an instance of the filesystem for this server.
func (s *Server) Filesystem() *filesystem.Filesystem {
	return s.fs
}

// EnsureDataDirectoryExists ensures that the data directory for the server
// instance exists.
func (s *Server) EnsureDataDirectoryExists() error {
	if _, err := os.Lstat(s.fs.Path()); err != nil {
		if os.IsNotExist(err) {
			s.Log().Debug("server: creating root directory and setting permissions")
			if err := os.MkdirAll(s.fs.Path(), 0700); err != nil {
				return errors.WithStack(err)
			}
			if err := s.fs.Chown("/"); err != nil {
				s.Log().WithField("error", err).Warn("server: failed to chown server data directory")
			}
		} else {
			return errors.WrapIf(err, "server: failed to stat server root directory")
		}
	}
	return nil
}

// Sets the state of the server internally. This function handles crash detection as
// well as reporting to event listeners for the server.
func (s *Server) OnStateChange() {
	prevState := s.resources.State.Load()

	st := s.Environment.State()
	// Update the currently tracked state for the server.
	s.resources.State.Store(st)

	// Emit the event to any listeners that are currently registered.
	if prevState != s.Environment.State() {
		s.Log().WithField("status", st).Debug("saw server status change event")
		s.Events().Publish(StatusEvent, st)
	}

	// Reset the resource usage to 0 when the process fully stops so that all of the UI
	// views in the Panel correctly display 0.
	if st == environment.ProcessOfflineState {
		s.resources.Reset()
		s.emitProcUsage()
	}

	// If server was in an online state, and is now in an offline state we should handle
	// that as a crash event. In that scenario, check the last crash time, and the crash
	// counter.
	//
	// In the event that we have passed the thresholds, don't do anything, otherwise
	// automatically attempt to start the process back up for the user. This is done in a
	// separate thread as to not block any actions currently taking place in the flow
	// that called this function.
	if (prevState == environment.ProcessStartingState || prevState == environment.ProcessRunningState) && s.Environment.State() == environment.ProcessOfflineState {
		s.Log().Info("detected server as entering a crashed state; running crash handler")

		go func(server *Server) {
			if err := server.handleServerCrash(); err != nil {
				if IsTooFrequentCrashError(err) {
					server.Log().Info("did not restart server after crash; occurred too soon after the last")
				} else {
					s.PublishConsoleOutputFromDaemon("Server crash was detected but an error occurred while handling it.")
					server.Log().WithField("error", err).Error("failed to handle server crash")
				}
			}
		}(s)
	}
}

// IsRunning determines if the server state is running or not. This is different
// than the environment state, it is simply the tracked state from this daemon
// instance, and not the response from Docker.
func (s *Server) IsRunning() bool {
	st := s.Environment.State()

	return st == environment.ProcessRunningState || st == environment.ProcessStartingState
}
