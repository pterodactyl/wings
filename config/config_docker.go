package config

import (
	"encoding/base64"
	"sort"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/registry"
	"github.com/goccy/go-json"
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
	NetworkMTU int64                   `default:"1500" yaml:"network_mtu"`
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

	// TmpfsSize specifies the size for the /tmp directory mounted into containers. Please be
	// aware that Docker utilizes the host's system memory for this value, and that we do not
	// keep track of the space used there, so avoid allocating too much to a server.
	TmpfsSize uint `default:"100" json:"tmpfs_size" yaml:"tmpfs_size"`

	// ContainerPidLimit sets the total number of processes that can be active in a container
	// at any given moment. This is a security concern in shared-hosting environments where a
	// malicious process could create enough processes to cause the host node to run out of
	// available pids and crash.
	ContainerPidLimit int64 `default:"512" json:"container_pid_limit" yaml:"container_pid_limit"`

	// InstallerLimits defines the limits on the installer containers that prevents a server's
	// installation process from unintentionally consuming more resources than expected. This
	// is used in conjunction with the server's defined limits. Whichever value is higher will
	// take precedence in the installer containers.
	InstallerLimits struct {
		Memory int64 `default:"1024" json:"memory" yaml:"memory"`
		Cpu    int64 `default:"100" json:"cpu" yaml:"cpu"`
	} `json:"installer_limits" yaml:"installer_limits"`

	// Overhead controls the memory overhead given to all containers to circumvent certain
	// software such as the JVM not staying below the maximum memory limit.
	Overhead Overhead `json:"overhead" yaml:"overhead"`

	UsePerformantInspect bool `default:"true" json:"use_performant_inspect" yaml:"use_performant_inspect"`

	// Sets the user namespace mode for the container when user namespace remapping option is
	// enabled.
	//
	// If the value is blank, the daemon's user namespace remapping configuration is used,
	// if the value is "host", then the pterodactyl containers are started with user namespace
	// remapping disabled
	UsernsMode string `default:"" json:"userns_mode" yaml:"userns_mode"`

	LogConfig struct {
		Type   string            `default:"local" json:"type" yaml:"type"`
		Config map[string]string `default:"{\"max-size\":\"5m\",\"max-file\":\"1\",\"compress\":\"false\",\"mode\":\"non-blocking\"}" json:"config" yaml:"config"`
	} `json:"log_config" yaml:"log_config"`
}

func (c DockerConfiguration) ContainerLogConfig() container.LogConfig {
	if c.LogConfig.Type == "" {
		return container.LogConfig{}
	}

	return container.LogConfig{
		Type:   c.LogConfig.Type,
		Config: c.LogConfig.Config,
	}
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
	b, err := json.Marshal(registry.AuthConfig{
		Username: c.Username,
		Password: c.Password,
	})
	if err != nil {
		return "", err
	}
	return base64.URLEncoding.EncodeToString(b), nil
}

// Overhead controls the memory overhead given to all containers to circumvent certain
// software such as the JVM not staying below the maximum memory limit.
type Overhead struct {
	// Override controls if the overhead limits should be overridden by the values in the config file.
	Override bool `default:"false" json:"override" yaml:"override"`

	// DefaultMultiplier sets the default multiplier for if no Multipliers are able to be applied.
	DefaultMultiplier float64 `default:"1.05" json:"default_multiplier" yaml:"default_multiplier"`

	// Multipliers allows overriding DefaultMultiplier depending on the amount of memory
	// configured for a server.
	//
	// Default values (used if Override is `false`)
	// - Less than 2048 MB of memory, multiplier of 1.15 (15%)
	// - Less than 4096 MB of memory, multiplier of 1.10 (10%)
	// - Otherwise, multiplier of 1.05 (5%) - specified in DefaultMultiplier
	//
	// If the defaults were specified in the config they would look like:
	// ```yaml
	// multipliers:
	//   2048: 1.15
	//   4096: 1.10
	// ```
	Multipliers map[int]float64 `json:"multipliers" yaml:"multipliers"`
}

func (o Overhead) GetMultiplier(memoryLimit int64) float64 {
	// Default multiplier values.
	if !o.Override {
		if memoryLimit <= 2048 {
			return 1.15
		} else if memoryLimit <= 4096 {
			return 1.10
		}
		return 1.05
	}

	// This plucks the keys of the Multipliers map, so they can be sorted from
	// smallest to largest in order to correctly apply the proper multiplier.
	i := 0
	multipliers := make([]int, len(o.Multipliers))
	for k := range o.Multipliers {
		multipliers[i] = k
		i++
	}
	sort.Ints(multipliers)

	// Loop through the memory values in order (smallest to largest)
	for _, m := range multipliers {
		// If the server's memory limit exceeds the modifier's limit, don't apply it.
		if memoryLimit > int64(m) {
			continue
		}
		return o.Multipliers[m]
	}

	return o.DefaultMultiplier
}
