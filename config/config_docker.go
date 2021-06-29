package config

import (
	"encoding/base64"
	"encoding/json"

	"github.com/docker/docker/api/types"
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

// DockerConfiguration defines the docker configuration used by the daemon when
// interacting with containers and networks on the system.
type DockerConfiguration struct {
	// Network configuration that should be used when creating a new network
	// for containers run through the daemon.
	Network DockerNetworkConfiguration `json:"network" yaml:"network"`

	// Domainname is the Docker domainname for all containers.
	Domainname string `default:"" json:"domainname" yaml:"domainname"`

	// Registries .
	Registries map[string]RegistryConfiguration `json:"registries" yaml:"registries"`

	// The size of the /tmp directory when mounted into a container. Please be aware that Docker
	// utilizes host memory for this value, and that we do not keep track of the space used here
	// so avoid allocating too much to a server.
	TmpfsSize uint `default:"100" json:"tmpfs_size" yaml:"tmpfs_size"`

	// ContainerPidLimit sets the total number of processes that can be active in a container
	// at any given moment. This is a security concern in shared-hosting environments where a
	// malicious process could create enough processes to cause the host node to run out of
	// available pids and crash.
	ContainerPidLimit int64 `default:"512" json:"container_pid_limit" yaml:"container_pid_limit"`

	// InstallLimits defines the limits on the installer containers that prevents a server's
	// installation process from unintentionally consuming more resources than expected. This
	// is used in conjunction with the server's defined limits. Whichever value is higher will
	// take precedence in the install containers.
	InstallerLimits struct {
		Memory int64 `default:"1024" json:"memory" yaml:"memory"`
		Cpu    int64 `default:"100" json:"cpu" yaml:"cpu"`
	} `json:"installer_limits" yaml:"installer_limits"`
}

// RegistryConfiguration defines the authentication credentials for a given
// Docker registry.
type RegistryConfiguration struct {
	Username string `yaml:"username"`
	Password string `yaml:"password"`
}

// Base64 returns the authentication for a given registry as a base64 encoded
// string value.
func (c RegistryConfiguration) Base64() (string, error) {
	b, err := json.Marshal(types.AuthConfig{
		Username: c.Username,
		Password: c.Password,
	})
	if err != nil {
		return "", err
	}
	return base64.URLEncoding.EncodeToString(b), nil
}
