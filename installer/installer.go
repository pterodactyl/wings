package installer

import (
	"encoding/json"
	"github.com/asaskevich/govalidator"
	"github.com/buger/jsonparser"
	"github.com/pkg/errors"
	"github.com/pterodactyl/wings/config"
	"github.com/pterodactyl/wings/server"
	"go.uber.org/zap"
)

type Installer struct {
	server *server.Server
}

// Validates the received data to ensure that all of the required fields
// have been passed along in the request. This should be manually run before
// calling Execute().
func New(data []byte) (*Installer, error) {
	if !govalidator.IsUUIDv4(getString(data, "uuid")) {
		return nil, errors.New("uuid provided was not in a valid format")
	}

	if !govalidator.IsUUIDv4(getString(data, "service", "egg")) {
		return nil, errors.New("service egg provided was not in a valid format")
	}

	s := &server.Server{
		Uuid:       getString(data, "uuid"),
		Suspended:  false,
		State:      server.ProcessOfflineState,
		Invocation: getString(data, "invocation"),
		EnvVars:    make(map[string]string),
		Build: server.BuildSettings{
			MemoryLimit: getInt(data, "build", "memory"),
			Swap:        getInt(data, "build", "swap"),
			IoWeight:    uint16(getInt(data, "build", "io")),
			CpuLimit:    getInt(data, "build", "cpu"),
			DiskSpace:   getInt(data, "build", "disk"),
		},
		Allocations: server.Allocations{
			Mappings: make(map[string][]int),
		},
	}

	s.Init()

	s.Allocations.DefaultMapping.Ip = getString(data, "allocations", "default", "ip")
	s.Allocations.DefaultMapping.Port = int(getInt(data, "allocations", "default", "port"))

	// Unmarshal the environment variables from the request into the server struct.
	if b, _, _, err := jsonparser.Get(data, "environment"); err != nil {
		return nil, errors.WithStack(err)
	} else {
		s.EnvVars = make(map[string]string)
		if err := json.Unmarshal(b, &s.EnvVars); err != nil {
			return nil, errors.WithStack(err)
		}
	}

	// Unmarshal the allocation mappings from the request into the server struct.
	if b, _, _, err := jsonparser.Get(data, "allocations", "mappings"); err != nil {
		return nil, errors.WithStack(err)
	} else {
		s.Allocations.Mappings = make(map[string][]int)
		if err := json.Unmarshal(b, &s.Allocations.Mappings); err != nil {
			return nil, errors.WithStack(err)
		}
	}

	s.Container.Image = getString(data, "container", "image")

	b, err := s.WriteConfigurationToDisk()
	if err != nil {
		return nil, err
	}

	// Destroy the temporary server instance.
	s = nil

	// Create a new server instance using the configuration we wrote to the disk
	// so that everything gets instantiated correctly on the struct.
	s2, err := server.FromConfiguration(b, &config.Get().System)

	return &Installer{
		server: s2,
	}, err
}

// Returns the UUID associated with this installer instance.
func (i *Installer) Uuid() string {
	return i.server.Uuid
}

// Return the server instance.
func (i *Installer) Server() *server.Server {
	return i.server
}

// Executes the installer process, creating the server and running through the
// associated installation process based on the parameters passed through for
// the server instance.
func (i *Installer) Execute() {
	zap.S().Debugw("creating required environment for server instance", zap.String("server", i.Uuid()))
	if err := i.server.Environment.Create(); err != nil {
		zap.S().Errorw("failed to create environment for server", zap.String("server", i.Uuid()), zap.Error(err))
		return
	}

	zap.S().Debugw("created environment for server during install process", zap.String("server", i.Uuid()))
}

// Returns a string value from the JSON data provided.
func getString(data []byte, key ...string) string {
	value, _ := jsonparser.GetString(data, key...)

	return value
}

// Returns an int value from the JSON data provided.
func getInt(data []byte, key ...string) int64 {
	value, _ := jsonparser.GetInt(data, key...)

	return value
}
