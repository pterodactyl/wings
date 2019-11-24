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
func New(data []byte) (error, *Installer) {
	if !govalidator.IsUUIDv4(getString(data, "uuid")) {
		return errors.New("uuid provided was not in a valid format"), nil
	}

	if !govalidator.IsUUIDv4(getString(data, "service", "egg")) {
		return errors.New("service egg provided was not in a valid format"), nil
	}

	s := &server.Server{
		Uuid:       getString(data, "uuid"),
		Suspended:  false,
		State:      server.ProcessOfflineState,
		Invocation: "",
		EnvVars:    make(map[string]string),
		Build: &server.BuildSettings{
			MemoryLimit: getInt(data, "build", "memory"),
			Swap:        getInt(data, "build", "swap"),
			IoWeight:    uint16(getInt(data, "build", "io")),
			CpuLimit:    getInt(data, "build", "cpu"),
			DiskSpace:   getInt(data, "build", "disk"),
		},
		Allocations: &server.Allocations{
			Mappings: make(map[string][]int),
		},
	}

	s.Init()

	s.Allocations.DefaultMapping.Ip = getString(data, "allocations", "default", "ip")
	s.Allocations.DefaultMapping.Port = int(getInt(data, "allocations", "default", "port"))

	jsonparser.ObjectEach(data, func(key []byte, value []byte, dataType jsonparser.ValueType, offset int) error {
		s.EnvVars[string(key)] = string(value)

		return nil
	}, "environment")

	jsonparser.ArrayEach(data, func(value []byte, dataType jsonparser.ValueType, offset int, err error) {
		var dat map[string][]int
		if err := json.Unmarshal(value, &dat); err != nil {
			return
		}

		for i, k := range dat {
			s.Allocations.Mappings[i] = k
		}
	}, "allocations", "mappings")

	s.Container.Image = getString(data, "container", "image")

	b, err := s.WriteConfigurationToDisk()
	if err != nil {
		return err, nil
	}

	// Destroy the temporary server instance.
	s = nil

	// Create a new server instance using the configuration we wrote to the disk
	// so that everything gets instantiated correctly on the struct.
	s2, err := server.FromConfiguration(b, config.Get().System)

	return nil, &Installer{
		server: s2,
	}
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
	zap.S().Debugw("beginning installation process for server", zap.String("server", i.server.Uuid))

	zap.S().Debugw("creating required environment for server instance", zap.String("server", i.server.Uuid))
	if err := i.server.Environment.Create(); err != nil {
		zap.S().Errorw("failed to create environment for server", zap.String("server", i.server.Uuid), zap.Error(err))
		return
	}
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
