package installer

import (
	"encoding/json"
	"github.com/apex/log"
	"github.com/asaskevich/govalidator"
	"github.com/buger/jsonparser"
	"github.com/pkg/errors"
	"github.com/pterodactyl/wings/api"
	"github.com/pterodactyl/wings/config"
	"github.com/pterodactyl/wings/server"
	"os"
	"path"
)

type Installer struct {
	server *server.Server
}

// Validates the received data to ensure that all of the required fields
// have been passed along in the request. This should be manually run before
// calling Execute().
func New(data []byte) (*Installer, error) {
	if !govalidator.IsUUIDv4(getString(data, "uuid")) {
		return nil, NewValidationError("uuid provided was not in a valid format")
	}

	if !govalidator.IsUUIDv4(getString(data, "service", "egg")) {
		return nil, NewValidationError("service egg provided was not in a valid format")
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
			Threads:     getString(data, "build", "threads"),
		},
		Allocations: server.Allocations{
			Mappings: make(map[string][]int),
		},
	}

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

	c, rerr, err := api.NewRequester().GetServerConfiguration(s.Uuid)
	if err != nil || rerr != nil {
		if err != nil {
			return nil, errors.WithStack(err)
		}

		return nil, errors.New(rerr.String())
	}

	// Destroy the temporary server instance.
	s = nil

	// Create a new server instance using the configuration we wrote to the disk
	// so that everything gets instantiated correctly on the struct.
	s2, err := server.FromConfiguration(c)

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
	p := path.Join(config.Get().System.Data, i.Uuid())
	l := log.WithFields(log.Fields{"server": i.Uuid(), "process": "installer"})

	l.WithField("path", p).Debug("creating required server data directory")
	if err := os.MkdirAll(p, 0755); err != nil {
		l.WithFields(log.Fields{"path": p, "error": errors.WithStack(err)}).Error("failed to create server data directory")
		return
	}

	if err := os.Chown(p, config.Get().System.User.Uid, config.Get().System.User.Gid); err != nil {
		l.WithField("error", errors.WithStack(err)).Error("failed to chown server data directory")
		return
	}

	l.Debug("creating required environment for server instance")
	if err := i.server.Environment.Create(); err != nil {
		l.WithField("error", err).Error("failed to create environment for server")
		return
	}

	l.Info("successfully created environment for server during install process")
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
