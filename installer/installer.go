package installer

import (
	"context"
	"encoding/json"

	"emperror.dev/errors"
	"github.com/asaskevich/govalidator"
	"github.com/buger/jsonparser"
	"github.com/pterodactyl/wings/environment"
	"github.com/pterodactyl/wings/remote"
	"github.com/pterodactyl/wings/server"
)

type Installer struct {
	server *server.Server
}

// New validates the received data to ensure that all of the required fields
// have been passed along in the request. This should be manually run before
// calling Execute().
func New(ctx context.Context, manager *server.Manager, data []byte) (*Installer, error) {
	if !govalidator.IsUUIDv4(getString(data, "uuid")) {
		return nil, NewValidationError("uuid provided was not in a valid format")
	}

	cfg := &server.Configuration{
		Uuid:              getString(data, "uuid"),
		Suspended:         false,
		Invocation:        getString(data, "invocation"),
		SkipEggScripts:    getBoolean(data, "skip_egg_scripts"),
		StartOnCompletion: getBoolean(data, "start_on_completion"),
		Build: environment.Limits{
			MemoryLimit: getInt(data, "build", "memory"),
			Swap:        getInt(data, "build", "swap"),
			IoWeight:    uint16(getInt(data, "build", "io")),
			CpuLimit:    getInt(data, "build", "cpu"),
			DiskSpace:   getInt(data, "build", "disk"),
			Threads:     getString(data, "build", "threads"),
		},
		CrashDetectionEnabled: true,
	}

	cfg.Allocations.DefaultMapping.Ip = getString(data, "allocations", "default", "ip")
	cfg.Allocations.DefaultMapping.Port = int(getInt(data, "allocations", "default", "port"))

	// Unmarshal the environment variables from the request into the server struct.
	if b, _, _, err := jsonparser.Get(data, "environment"); err != nil {
		return nil, errors.WithStackIf(err)
	} else {
		cfg.EnvVars = make(environment.Variables)
		if err := json.Unmarshal(b, &cfg.EnvVars); err != nil {
			return nil, errors.WrapIf(err, "installer: could not unmarshal environment variables for server")
		}
	}

	// Unmarshal the allocation mappings from the request into the server struct.
	if b, _, _, err := jsonparser.Get(data, "allocations", "mappings"); err != nil {
		return nil, errors.WithStackIf(err)
	} else {
		cfg.Allocations.Mappings = make(map[string][]int)
		if err := json.Unmarshal(b, &cfg.Allocations.Mappings); err != nil {
			return nil, errors.Wrap(err, "installer: could not unmarshal allocation mappings")
		}
	}

	cfg.Container.Image = getString(data, "container", "image")

	c, err := manager.Client().GetServerConfiguration(ctx, cfg.Uuid)
	if err != nil {
		if !remote.IsRequestError(err) {
			return nil, errors.WithStackIf(err)
		}
		return nil, errors.WrapIf(err, "installer: could not get server configuration from remote API")
	}

	// Create a new server instance using the configuration we wrote to the disk
	// so that everything gets instantiated correctly on the struct.
	s, err := manager.InitServer(c)
	if err != nil {
		return nil, errors.WrapIf(err, "installer: could not init server instance")
	}
	return &Installer{server: s}, nil
}

// Uuid returns the UUID associated with this installer instance.
func (i *Installer) Uuid() string {
	return i.server.Id()
}

// Server returns the server instance.
func (i *Installer) Server() *server.Server {
	return i.server
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

func getBoolean(data []byte, key ...string) bool {
	value, _ := jsonparser.GetBoolean(data, key...)

	return value
}
