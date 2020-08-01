package config

import (
	"encoding/base64"
	"encoding/json"
	"github.com/docker/docker/api/types"
	"github.com/pkg/errors"
)

type dockerNetworkInterfaces struct {
	V4 struct {
		Subnet  string `default:"172.18.0.0/16"`
		Gateway string `default:"172.18.0.1"`
	}

	V6 struct {
		Subnet  string `default:"fdba:17c8:6c94::/64"`
		Gateway string `default:"fdba:17c8:6c94::1011"`
	}
}

type DockerNetworkConfiguration struct {
	// The interface that should be used to create the network. Must not conflict
	// with any other interfaces in use by Docker or on the system.
	Interface string `default:"172.18.0.1" json:"interface" yaml:"interface"`

	// The DNS settings for containers.
	Dns []string `default:"[\"1.1.1.1\", \"1.0.0.1\"]"`

	// The name of the network to use. If this network already exists it will not
	// be created. If it is not found, a new network will be created using the interface
	// defined.
	Name       string                  `default:"pterodactyl_nw"`
	ISPN       bool                    `default:"false" yaml:"ispn"`
	Driver     string                  `default:"bridge"`
	Mode       string                  `default:"pterodactyl_nw" yaml:"network_mode"`
	IsInternal bool                    `default:"false" yaml:"is_internal"`
	EnableICC  bool                    `default:"true" yaml:"enable_icc"`
	Interfaces dockerNetworkInterfaces `yaml:"interfaces"`
}

// Defines the docker configuration used by the daemon when interacting with
// containers and networks on the system.
type DockerConfiguration struct {
	// Network configuration that should be used when creating a new network
	// for containers run through the daemon.
	Network DockerNetworkConfiguration `json:"network" yaml:"network"`

	// Domainname is the Docker domainname for all containers.
	Domainname string `default:"" json:"domainname" yaml:"domainname"`

	// If true, container images will be updated when a server starts if there
	// is an update available. If false the daemon will not attempt updates and will
	// defer to the host system to manage image updates.
	UpdateImages bool `default:"true" json:"update_images" yaml:"update_images"`

	// The location of the Docker socket.
	Socket string `default:"/var/run/docker.sock" json:"socket" yaml:"socket"`

	// Defines the location of the timezone file on the host system that should
	// be mounted into the created containers so that they all use the same time.
	TimezonePath string `default:"/etc/timezone" json:"timezone_path" yaml:"timezone_path"`

	// Registries .
	Registries map[string]RegistryConfiguration `json:"registries" yaml:"registries"`
}

// RegistryConfiguration .
type RegistryConfiguration struct {
	Username string `yaml:"username"`
	Password string `yaml:"password"`
}

// Base64 .
func (c RegistryConfiguration) Base64() (string, error) {
	authConfig := types.AuthConfig{
		Username: c.Username,
		Password: c.Password,
	}

	b, err := json.Marshal(authConfig)
	if err != nil {
		return "", errors.Wrap(err, "failed to marshal AuthConfig")
	}

	return base64.URLEncoding.EncodeToString(b), nil
}
