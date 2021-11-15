package environment

import (
	"context"
	"strconv"
	"sync"

	"emperror.dev/errors"
	"github.com/apex/log"
	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"

	"github.com/pterodactyl/wings/config"
)

var (
	_conce  sync.Once
	_client *client.Client
)

// Docker returns a docker client to be used throughout the codebase. Once a
// client has been created it will be returned for all subsequent calls to this
// function.
func Docker() (*client.Client, error) {
	var err error
	_conce.Do(func() {
		_client, err = client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	})
	return _client, errors.Wrap(err, "environment/docker: could not create client")
}

// ConfigureDocker configures the required network for the docker environment.
func ConfigureDocker(ctx context.Context) error {
	// Ensure the required docker network exists on the system.
	cli, err := Docker()
	if err != nil {
		return err
	}

	nw := config.Get().Docker.Network
	resource, err := cli.NetworkInspect(ctx, nw.Name, types.NetworkInspectOptions{})
	if err != nil {
		if client.IsErrNotFound(err) {
			log.Info("creating missing pterodactyl0 interface, this could take a few seconds...")
			if err := createDockerNetwork(ctx, cli); err != nil {
				return err
			}
		} else {
			return err
		}
	}

	config.Update(func(c *config.Configuration) {
		c.Docker.Network.Driver = resource.Driver
		switch c.Docker.Network.Driver {
		case "host":
			c.Docker.Network.Interface = "127.0.0.1"
			c.Docker.Network.ISPN = false
		case "overlay":
			fallthrough
		case "weavemesh":
			c.Docker.Network.Interface = ""
			c.Docker.Network.ISPN = true
		default:
			c.Docker.Network.ISPN = false
		}
	})
	return nil
}

// Creates a new network on the machine if one does not exist already.
func createDockerNetwork(ctx context.Context, cli *client.Client) error {
	nw := config.Get().Docker.Network
	_, err := cli.NetworkCreate(ctx, nw.Name, types.NetworkCreate{
		Driver:     nw.Driver,
		EnableIPv6: true,
		Internal:   nw.IsInternal,
		IPAM: &network.IPAM{
			Config: []network.IPAMConfig{{
				Subnet:  nw.Interfaces.V4.Subnet,
				Gateway: nw.Interfaces.V4.Gateway,
			}, {
				Subnet:  nw.Interfaces.V6.Subnet,
				Gateway: nw.Interfaces.V6.Gateway,
			}},
		},
		Options: map[string]string{
			"encryption": "false",
			"com.docker.network.bridge.default_bridge":       "false",
			"com.docker.network.bridge.enable_icc":           strconv.FormatBool(nw.EnableICC),
			"com.docker.network.bridge.enable_ip_masquerade": "true",
			"com.docker.network.bridge.host_binding_ipv4":    "0.0.0.0",
			"com.docker.network.bridge.name":                 "pterodactyl0",
			"com.docker.network.driver.mtu":                  "1500",
		},
	})
	if err != nil {
		return err
	}
	if nw.Driver != "host" && nw.Driver != "overlay" && nw.Driver != "weavemesh" {
		config.Update(func(c *config.Configuration) {
			c.Docker.Network.Interface = c.Docker.Network.Interfaces.V4.Gateway
		})
	}
	return nil
}
