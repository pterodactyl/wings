package server

import (
    "github.com/pterodactyl/wings"
    "github.com/pterodactyl/wings/environment"
    "gopkg.in/yaml.v2"
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

    EnvVars map[string]string `yaml:"env"`

    Build   *BuildSettings
    Network *Allocations

    environment *environment.Environment
}

// The build settings for a given server that impact docker container creation and
// resource limits for a server instance.
type BuildSettings struct {
    // The total amount of memory in megabytes that this server is allowed to
    // use on the host system.
    MemoryLimit int

    // The amount of additional swap space to be provided to a container instance.
    Swap int

    // The relative weight for IO operations in a container. This is relative to other
    // containers on the system and should be a value between 10 and 1000.
    IoWeight int

    // The percentage of CPU that this instance is allowed to consume relative to
    // the host. A value of 200% represents complete utilization of two cores. This
    // should be a value between 1 and THREAD_COUNT * 100.
    CpuLimit int

    // The amount of disk space in megabytes that a server is allowed to use.
    DiskSpace int
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
    }

    // Mappings contains all of the ports that should be assigned to a given server
    // attached to the IP they correspond to.
    Mappings map[string][]int
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
