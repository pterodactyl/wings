package server

import (
	"github.com/pterodactyl/wings"
	"github.com/pterodactyl/wings/environment"
	"github.com/remeh/sizedwaitgroup"
	"go.uber.org/zap"
	"gopkg.in/yaml.v2"
	"io/ioutil"
	"os"
	"strings"
)

// High level definition for a server instance being controlled by Wings.
type Server struct {
	// The unique identifier for the server that should be used when referencing
	// it aganist the Panel API (and internally). This will be used when naming
	// docker containers as well as in log output.
	Uuid string

	// Wether or not the server is in a suspended state. Suspended servers cannot
	// be started or modified except in certain scenarios by an admin user.
	Suspended bool

	// The power state of the server.
	State int

	// The command that should be used when booting up the server instance.
	Invocation string

	// An array of environment variables that should be passed along to the running
	// server process.
	EnvVars map[string]string `yaml:"env"`

	Build       *BuildSettings
	Allocations *Allocations

	environment *environment.Environment
}

// The build settings for a given server that impact docker container creation and
// resource limits for a server instance.
type BuildSettings struct {
	// The total amount of memory in megabytes that this server is allowed to
	// use on the host system.
	MemoryLimit int `yaml:"memory"`

	// The amount of additional swap space to be provided to a container instance.
	Swap int

	// The relative weight for IO operations in a container. This is relative to other
	// containers on the system and should be a value between 10 and 1000.
	IoWeight int `yaml:"io"`

	// The percentage of CPU that this instance is allowed to consume relative to
	// the host. A value of 200% represents complete utilization of two cores. This
	// should be a value between 1 and THREAD_COUNT * 100.
	CpuLimit int `yaml:"cpu"`

	// The amount of disk space in megabytes that a server is allowed to use.
	DiskSpace int `yaml:"disk"`
}

// Defines the allocations available for a given server. When using the Docker environment
// driver these correspond to mappings for the container that allow external connections.
type Allocations struct {
	// Defines the default allocation that should be used for this server. This is
	// what will be used for {SERVER_IP} and {SERVER_PORT} when modifying configuration
	// files or the startup arguments for a server.
	DefaultMapping struct {
		Ip   string
		Port int
	} `yaml:"default"`

	// Mappings contains all of the ports that should be assigned to a given server
	// attached to the IP they correspond to.
	Mappings map[string][]int
}

// Iterates over a given directory and loads all of the servers listed before returning
// them to the calling function.
func LoadDirectory(dir string) (*[]Server, error) {
	wg := sizedwaitgroup.New(5)

	f, err := ioutil.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	zap.S().Debug("starting loop")
	for _, file := range f {
		if !strings.HasSuffix(file.Name(), ".yml") {
			continue
		}

		wg.Add()
		go func(file os.FileInfo) {
			zap.S().Debugw("processing in parallel", zap.String("name", file.Name()))
			wg.Done()
		}(file)
	}

	wg.Wait()

	zap.S().Debug("done processing files")

	return nil, nil
}

// Initalizes a server using a data byte array. This will be marshaled into the
// given struct using a YAML marshaler. This will also configure the given environment
// for a server.
func FromConfiguration(data []byte, cfg wings.DockerConfiguration) (*Server, error) {
	s := &Server{}

	if err := yaml.Unmarshal(data, s); err != nil {
		return nil, err
	}

	// Right now we only support a Docker based environment, so I'm going to hard code
	// this logic in. When we're ready to support other environment we'll need to make
	// some modifications here obviously.
	var env environment.Environment
	env = &environment.Docker{
		Controller: &environment.Controller{
			Server: s,
		},
		Configuration: cfg,
	}

	s.environment = &env

	return s, nil
}
