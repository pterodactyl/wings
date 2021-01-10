package installer

import (
	"encoding/json"

	"emperror.dev/errors"
	"github.com/asaskevich/govalidator"
	"github.com/buger/jsonparser"
	"github.com/pterodactyl/wings/api"
	"github.com/pterodactyl/wings/environment"
	"github.com/pterodactyl/wings/server"
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

	cfg := &server.Configuration{
		Uuid:           getString(data, "uuid"),
		Suspended:      false,
		Invocation:     getString(data, "invocation"),
		SkipEggScripts: getBoolean(data, "skip_egg_scripts"),
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
		return nil, err
	} else {
		cfg.EnvVars = make(environment.Variables)
		if err := json.Unmarshal(b, &cfg.EnvVars); err != nil {
			return nil, err
		}
	}

	// Unmarshal the allocation mappings from the request into the server struct.
	if b, _, _, err := jsonparser.Get(data, "allocations", "mappings"); err != nil {
		return nil, err
	} else {
		cfg.Allocations.Mappings = make(map[string][]int)
		if err := json.Unmarshal(b, &cfg.Allocations.Mappings); err != nil {
			return nil, err
		}
	}

	cfg.Container.Image = getString(data, "container", "image")

	c, err := api.New().GetServerConfiguration(cfg.Uuid)
	if err != nil {
		if !api.IsRequestError(err) {
			return nil, err
		}

		return nil, errors.New(err.Error())
	}

	// Create a new server instance using the configuration we wrote to the disk
	// so that everything gets instantiated correctly on the struct.
	s, err := server.FromConfiguration(c)

	return &Installer{
		server: s,
	}, err
}

// Returns the UUID associated with this installer instance.
func (i *Installer) Uuid() string {
	return i.server.Id()
}

// Return the server instance.
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
