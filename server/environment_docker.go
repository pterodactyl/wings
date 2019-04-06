package server

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/client"
	"github.com/docker/docker/daemon/logger/jsonfilelog"
	"github.com/docker/go-connections/nat"
	"github.com/pkg/errors"
	"golang.org/x/net/context"
	"io"
	"os"
	"strconv"
	"strings"
	"time"
)

// Defines the base environment for Docker instances running through Wings.
type DockerEnvironment struct {
	Server *Server

	// The user that containers should be running as.
	User string

	// Defines the configuration for the Docker instance that will allow us to connect
	// and create and modify containers.
	TimezonePath string

	// The Docker client being used for this instance.
	Client *client.Client
}

// Creates a new base Docker environment. A server must still be attached to it.
func NewDockerEnvironment(opts ...func(*DockerEnvironment)) (*DockerEnvironment, error) {
	cli, err := client.NewClientWithOpts(client.FromEnv)
	if err != nil {
		return nil, err
	}

	env := &DockerEnvironment{
		Client: cli,
	}

	for _, opt := range opts {
		opt(env)
	}

	return env, nil
}

// Ensure that the Docker environment is always implementing all of the methods
// from the base environment interface.
var _ Environment = (*DockerEnvironment)(nil)

// Returns the name of the environment.
func (d *DockerEnvironment) Type() string {
	return "docker"
}

// Determines if the container exists in this environment.
func (d *DockerEnvironment) Exists() bool {
	_, err := d.Client.ContainerInspect(context.Background(), d.Server.Uuid)

	return err == nil
}

// Checks if there is a container that already exists for the server. If so that
// container is started. If there is no container, one is created and then started.
func (d *DockerEnvironment) Start() error {
	c, err := d.Client.ContainerInspect(context.Background(), d.Server.Uuid)
	if err != nil {
		// @todo what?
		return err
	}

	// No reason to try starting a container that is already running.
	if c.State.Running {
		return nil
	}

	opts := types.ContainerStartOptions{}

	return d.Client.ContainerStart(context.Background(), d.Server.Uuid, opts)
}

// Stops the container that the server is running in. This will allow up to 10
// seconds to pass before a failure occurs.
func (d *DockerEnvironment) Stop() error {
	t := time.Second * 10

	return d.Client.ContainerStop(context.Background(), d.Server.Uuid, &t)
}

// Forcefully terminates the container using the signal passed through.
func (d *DockerEnvironment) Terminate(signal os.Signal) error {
	ctx := context.Background()

	c, err := d.Client.ContainerInspect(ctx, d.Server.Uuid)
	if err != nil {
		return err
	}

	if !c.State.Running {
		return nil
	}

	return d.Client.ContainerKill(ctx, d.Server.Uuid, signal.String())
}

// Contrary to the name, this doesn't actually attach to the Docker container itself,
// but rather attaches to the log for the container and then pipes that output to
// a websocket.
//
// This avoids us missing cruicial output that happens in the split seconds before the
// code moves from 'Starting' to 'Attaching' on the process.
//
// @todo add throttle code
func (d *DockerEnvironment) Attach() (io.ReadCloser, error) {
	if !d.Exists() {
		return nil, errors.New(fmt.Sprintf("no such container: %s", d.Server.Uuid))
	}

	ctx := context.Background()
	opts := types.ContainerLogsOptions{
		ShowStderr: true,
		ShowStdout: true,
		Follow:     true,
	}

	r, err := d.Client.ContainerLogs(ctx, d.Server.Uuid, opts)

	return r, err
}

// Creates a new container for the server using all of the data that is currently
// available for it. If the container already exists it will be returned.
func (d *DockerEnvironment) Create() error {
	ctx := context.Background()
	cli, err := client.NewClientWithOpts(client.FromEnv)
	if err != nil {
		return err
	}

	var oomDisabled = true

	// If the container already exists don't hit the user with an error, just return
	// the current information about it which is what we would do when creating the
	// container anyways.
	if _, err := cli.ContainerInspect(ctx, d.Server.Uuid); err == nil {
		return nil
	}

	conf := &container.Config{
		Hostname:     "container",
		User:         d.User,
		AttachStdin:  true,
		AttachStdout: true,
		AttachStderr: true,
		OpenStdin:    true,
		Tty:          true,

		ExposedPorts: d.exposedPorts(),

		Image: d.Server.Container.Image,
		Env:   d.environmentVariables(),

		Labels: map[string]string{
			"Service": "Pterodactyl",
		},
	}

	hostConf := &container.HostConfig{
		PortBindings: d.portBindings(),

		// Configure the mounts for this container. First mount the server data directory
		// into the container as a r/w bind. Additionally mount the host timezone data into
		// the container as a readonly bind so that software running in the container uses
		// the same time as the host system.
		Mounts: []mount.Mount{
			{
				Target:   "/home/container",
				Source:   d.Server.Filesystem().Path(),
				Type:     mount.TypeBind,
				ReadOnly: false,
			},
			{
				Target:   d.TimezonePath,
				Source:   d.TimezonePath,
				Type:     mount.TypeBind,
				ReadOnly: true,
			},
		},

		// Configure the /tmp folder mapping in containers. This is necessary for some
		// games that need to make use of it for downloads and other installation processes.
		Tmpfs: map[string]string{
			"/tmp": "rw,exec,nosuid,size=50M",
		},

		// Define resource limits for the container based on the data passed through
		// from the Panel.
		Resources: container.Resources{
			// @todo memory limit should be slightly higher than the reservation
			Memory:            d.Server.Build.MemoryLimit * 1000000,
			MemoryReservation: d.Server.Build.MemoryLimit * 1000000,
			MemorySwap:        d.Server.Build.ConvertedSwap(),

			CPUQuota:  d.Server.Build.ConvertedCpuLimit(),
			CPUPeriod: 100000,
			CPUShares: 1024,

			BlkioWeight:    d.Server.Build.IoWeight,
			OomKillDisable: &oomDisabled,
		},

		// @todo make this configurable again
		DNS: []string{"1.1.1.1", "8.8.8.8"},

		// Configure logging for the container to make it easier on the Daemon to grab
		// the server output. Ensure that we don't use too much space on the host machine
		// since we only need it for the last few hundred lines of output and don't care
		// about anything else in it.
		LogConfig: container.LogConfig{
			Type: jsonfilelog.Name,
			Config: map[string]string{
				"max-size": "5m",
				"max-file": "1",
			},
		},

		SecurityOpt:    []string{"no-new-privileges"},
		ReadonlyRootfs: true,
		CapDrop: []string{
			"setpcap", "mknod", "audit_write", "net_raw", "dac_override",
			"fowner", "fsetid", "net_bind_service", "sys_chroot", "setfcap",
		},
		NetworkMode: "pterodactyl_nw",
	}

	if _, err := cli.ContainerCreate(ctx, conf, hostConf, nil, d.Server.Uuid); err != nil {
		return err
	}

	return nil
}

// Reads the log file for the server. This does not care if the server is running or not, it will
// simply try to read the last X bytes of the file and return them.
func (d *DockerEnvironment) Readlog(len int64) ([]string, error) {
	ctx := context.Background()

	j, err := d.Client.ContainerInspect(ctx, d.Server.Uuid)
	if err != nil {
		return nil, err
	}

	if j.LogPath == "" {
		return nil, errors.New("empty log path defined for server")
	}

	f, err := os.Open(j.LogPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	// Check if the length of the file is smaller than the amount of data that was requested
	// for reading. If so, adjust the length to be the total length of the file. If this is not
	// done an error is thrown since we're reading backwards, and not forwards.
	if stat, err := os.Stat(j.LogPath); err != nil {
		return nil, err
	} else if stat.Size() < len {
		len = stat.Size()
	}

	// Seed to the end of the file and then move backwards until the length is met to avoid
	// reading the entirety of the file into memory.
	if _, err := f.Seek(-len, io.SeekEnd); err != nil {
		return nil, err
	}

	b := make([]byte, len)

	if _, err := f.Read(b); err != nil && err != io.EOF {
		return nil, err
	}

	return d.parseLogToStrings(b)
}

type dockerLogLine struct {
	Log    string `json:"log"`
}

// Docker stores the logs for server output in a JSON format. This function will iterate over the JSON
// that was read from the log file and parse it into a more human readable format.
func (d *DockerEnvironment) parseLogToStrings(b []byte) ([]string, error) {
	var hasError = false
	var out []string

	scanner := bufio.NewScanner(bytes.NewReader(b))
	for scanner.Scan() {
		var l dockerLogLine
		// Unmarshal the contents and allow up to a single error before bailing out of the process. We
		// do this because if you're arbitrarily reading a length of the file you'll likely end up
		// with the first line in the output being improperly formatted JSON. In those cases we want to
		// just skip over it. However if we see another error we're going to bail out because that is an
		// abnormal situation.
		if err := json.Unmarshal([]byte(scanner.Text()), &l); err != nil {
			if hasError {
				return nil, err
			}

			hasError = true
			continue
		}

		out = append(out, l.Log)
	}

	return out, nil
}

// Returns the environment variables for a server in KEY="VALUE" form.
func (d *DockerEnvironment) environmentVariables() []string {
	var out = []string{
		fmt.Sprintf("STARTUP=%s", d.Server.Invocation),
		fmt.Sprintf("SERVER_MEMORY=%d", d.Server.Build.MemoryLimit),
		fmt.Sprintf("SERVER_IP=%s", d.Server.Allocations.DefaultMapping.Ip),
		fmt.Sprintf("SERVER_PORT=%d", d.Server.Allocations.DefaultMapping.Port),
	}

	for k, v := range d.Server.EnvVars {
		if strings.ToUpper(k) == "STARTUP" {
			continue
		}

		out = append(out, fmt.Sprintf("%s=\"%s\"", strings.ToUpper(k), v))
	}

	return out
}

func (d *DockerEnvironment) volumes() map[string]struct{} {
	return nil
}

// Converts the server allocation mappings into a format that can be understood
// by Docker.
func (d *DockerEnvironment) portBindings() nat.PortMap {
	var out = nat.PortMap{}

	for ip, ports := range d.Server.Allocations.Mappings {
		for _, port := range ports {
			// Skip over invalid ports.
			if port < 0 || port > 65535 {
				continue
			}

			binding := []nat.PortBinding{
				{
					HostIP:   ip,
					HostPort: strconv.Itoa(port),
				},
			}

			out[nat.Port(fmt.Sprintf("%d/tcp", port))] = binding
			out[nat.Port(fmt.Sprintf("%d/udp", port))] = binding
		}
	}

	return out
}

// Converts the server allocation mappings into a PortSet that can be understood
// by Docker. This formatting is slightly different than portBindings as it should
// return an empty struct rather than a binding.
//
// To accomplish this, we'll just get the values from portBindings and then set them
// to empty structs. Because why not.
func (d *DockerEnvironment) exposedPorts() nat.PortSet {
	var out = nat.PortSet{}

	for port := range d.portBindings() {
		out[port] = struct{}{}
	}

	return out
}
