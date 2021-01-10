package environment

import (
	"context"
	"strconv"
	"sync"

	"github.com/apex/log"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
	"github.com/pterodactyl/wings/config"
)

var _cmu sync.Mutex
var _client *client.Client

// Return a Docker client to be used throughout the codebase. Once a client has been created it
// will be returned for all subsequent calls to this function.
func DockerClient() (*client.Client, error) {
	_cmu.Lock()
	defer _cmu.Unlock()

	if _client != nil {
		return _client, nil
	}

	_client, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())

	return _client, err
}

// Configures the required network for the docker environment.
func ConfigureDocker(c *config.DockerConfiguration) error {
	// Ensure the required docker network exists on the system.
	cli, err := DockerClient()
	if err != nil {
		return err
	}

	resource, err := cli.NetworkInspect(context.Background(), c.Network.Name, types.NetworkInspectOptions{})
	if err != nil && client.IsErrNotFound(err) {
		log.Info("creating missing pterodactyl0 interface, this could take a few seconds...")
		return createDockerNetwork(cli, c)
	} else if err != nil {
		log.WithField("error", err).Fatal("failed to create required docker network for containers")
	}

	switch resource.Driver {
	case "host":
		c.Network.Interface = "127.0.0.1"
		c.Network.ISPN = false
		return nil
	case "overlay":
	case "weavemesh":
		c.Network.Interface = ""
		c.Network.ISPN = true
		return nil
	default:
		c.Network.ISPN = false
	}

	return nil
}

// Creates a new network on the machine if one does not exist already.
func createDockerNetwork(cli *client.Client, c *config.DockerConfiguration) error {
	_, err := cli.NetworkCreate(context.Background(), c.Network.Name, types.NetworkCreate{
		Driver:     c.Network.Driver,
		EnableIPv6: true,
		Internal:   c.Network.IsInternal,
		IPAM: &network.IPAM{
			Config: []network.IPAMConfig{
				{
					Subnet:  c.Network.Interfaces.V4.Subnet,
					Gateway: c.Network.Interfaces.V4.Gateway,
				},
				{
					Subnet:  c.Network.Interfaces.V6.Subnet,
					Gateway: c.Network.Interfaces.V6.Gateway,
				},
			},
		},
		Options: map[string]string{
			"encryption": "false",
			"com.docker.network.bridge.default_bridge":       "false",
			"com.docker.network.bridge.enable_icc":           strconv.FormatBool(c.Network.EnableICC),
			"com.docker.network.bridge.enable_ip_masquerade": "true",
			"com.docker.network.bridge.host_binding_ipv4":    "0.0.0.0",
			"com.docker.network.bridge.name":                 "pterodactyl0",
			"com.docker.network.driver.mtu":                  "1500",
		},
	})

	if err != nil {
		return err
	}

	switch c.Network.Driver {
	case "host":
		c.Network.Interface = "127.0.0.1"
		c.Network.ISPN = false
		break
	case "overlay":
	case "weavemesh":
		c.Network.Interface = ""
		c.Network.ISPN = true
		break
	default:
		c.Network.Interface = c.Network.Interfaces.V4.Gateway
		c.Network.ISPN = false
		break
	}

	return nil
}
