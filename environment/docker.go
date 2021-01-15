package environment

import (
	"context"
	"strconv"
	"sync"

	"github.com/apex/log"
	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
)

var _conce sync.Once
var _client *client.Client

// DockerClient returns a Docker client to be used throughout the codebase. Once
// a client has been created it will be returned for all subsequent calls to this
// function.
func DockerClient() (*client.Client, error) {
	var err error
	_conce.Do(func() {
		_client, err = client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	})
	return _client, err
}

// ConfigureDocker configures the required network for the docker environment.
func ConfigureDocker(ctx context.Context) error {
	// Ensure the required docker network exists on the system.
	cli, err := DockerClient()
	if err != nil {
		return err
	}

	nw := viper.Sub("docker.network")
	resource, err := cli.NetworkInspect(ctx, nw.GetString("name"), types.NetworkInspectOptions{})
	if err != nil {
		if client.IsErrNotFound(err) {
			log.Info("creating missing pterodactyl0 interface, this could take a few seconds...")
			if err := createDockerNetwork(ctx, cli); err != nil {
				return err
			}
		}
		return err
	} else {
		nw.Set("driver", resource.Driver)
	}

	switch nw.GetString("driver") {
	case "host":
		nw.Set("interface", "127.0.0.1")
		nw.Set("ispn", false)
	case "overlay":
		fallthrough
	case "weavemesh":
		nw.Set("interface", "")
		nw.Set("ispn", true)
	default:
		nw.Set("ispn", false)
	}
	return nil
}

// Creates a new network on the machine if one does not exist already.
func createDockerNetwork(ctx context.Context, cli *client.Client) error {
	nw := viper.Sub("docker.network")
	_, err := cli.NetworkCreate(ctx, nw.GetString("name"), types.NetworkCreate{
		Driver:     nw.GetString("driver"),
		EnableIPv6: true,
		Internal:   nw.GetBool("is_internal"),
		IPAM: &network.IPAM{
			Config: []network.IPAMConfig{
				{
					Subnet:  nw.GetString("interfaces.v4.subnet"),
					Gateway: nw.GetString("interfaces.v4.gateway"),
				},
				{
					Subnet:  nw.GetString("interfaces.v6.subnet"),
					Gateway: nw.GetString("interfaces.v6.gateway"),
				},
			},
		},
		Options: map[string]string{
			"encryption": "false",
			"com.docker.network.bridge.default_bridge":       "false",
			"com.docker.network.bridge.enable_icc":           strconv.FormatBool(nw.GetBool("enable_icc")),
			"com.docker.network.bridge.enable_ip_masquerade": "true",
			"com.docker.network.bridge.host_binding_ipv4":    "0.0.0.0",
			"com.docker.network.bridge.name":                 "pterodactyl0",
			"com.docker.network.driver.mtu":                  "1500",
		},
	})
	driver := nw.GetString("driver")
	if driver != "host" && driver != "overlay" && driver != "weavemesh" {
		nw.Set("interface", nw.GetString("interfaces.v4.gateway"))
	}
	return err
}
