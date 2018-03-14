package control

import (
	"context"
	"io"
	"os"
	"strings"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
	"github.com/pterodactyl/wings/constants"
	log "github.com/sirupsen/logrus"
)

type dockerEnvironment struct {
	baseEnvironment

	client   *client.Client
	hires    types.HijackedResponse
	attached bool

	server *ServerStruct
}

// Ensure DockerEnvironment implements Environment
var _ Environment = &dockerEnvironment{}

// NewDockerEnvironment creates a new docker enviornment
// instance and connects to the docker client on the host system
// If the container is already running it will try to reattach
// to the running container
func NewDockerEnvironment(server *ServerStruct) (Environment, error) {
	env := dockerEnvironment{}

	env.server = server
	env.attached = false

	cli, err := client.NewEnvClient()
	env.client = cli
	ctx := context.TODO()
	cli.NegotiateAPIVersion(ctx)

	if err != nil {
		log.WithError(err).Fatal("Failed to connect to docker.")
		return nil, err
	}

	if env.server.DockerContainer.ID != "" {
		if err := env.inspectContainer(ctx); err != nil {
			log.WithError(err).Error("Failed to find the container with stored id, removing id.")
			env.server.DockerContainer.ID = ""
			env.server.Save()
		}
	}

	return &env, nil
}

func (env *dockerEnvironment) inspectContainer(ctx context.Context) error {
	_, err := env.client.ContainerInspect(ctx, env.server.DockerContainer.ID)
	return err
}

func (env *dockerEnvironment) attach() error {
	if env.attached {
		return nil
	}

	cw := ConsoleHandler{
		Websockets: env.server.websockets,
	}

	var err error
	env.hires, err = env.client.ContainerAttach(context.TODO(), env.server.DockerContainer.ID,
		types.ContainerAttachOptions{
			Stdin:  true,
			Stdout: true,
			Stderr: true,
			Stream: true,
		})

	if err != nil {
		log.WithField("server", env.server.ID).WithError(err).Error("Failed to attach to docker container.")
		return err
	}
	env.attached = true

	go func() {
		defer env.hires.Close()
		defer func() {
			env.attached = false
		}()
		io.Copy(cw, env.hires.Reader)
	}()

	return nil
}

// Create creates the docker container for the environment and applies all
// settings to it
func (env *dockerEnvironment) Create() error {
	log.WithField("server", env.server.ID).Debug("Creating docker environment")

	ctx := context.TODO()

	if err := env.pullImage(ctx); err != nil {
		log.WithError(err).WithField("image", env.server.GetService().DockerImage).WithField("server", env.server.ID).Error("Failed to pull docker image.")
		return err
	}

	if err := os.MkdirAll(env.server.dataPath(), constants.DefaultFolderPerms); err != nil {
		return err
	}

	// Create docker container
	// TODO: apply cpu, io, disk limits.

	containerConfig := &container.Config{
		Image:        env.server.GetService().DockerImage,
		Cmd:          strings.Split(env.server.StartupCommand, " "),
		AttachStdin:  true,
		OpenStdin:    true,
		AttachStdout: true,
		AttachStderr: true,
		Tty:          true,
		Hostname:     constants.DockerContainerPrefix + env.server.UUIDShort(),
	}

	containerHostConfig := &container.HostConfig{
		Resources: container.Resources{
			Memory:     env.server.Settings.Memory,
			MemorySwap: env.server.Settings.Swap,
		},
		// TODO: Allow custom binds via some kind of settings in the service
		Binds: []string{env.server.dataPath() + ":/home/container"},
		// TODO: Add port bindings
	}

	containerHostConfig.Memory = 0

	container, err := env.client.ContainerCreate(ctx, containerConfig, containerHostConfig, nil, constants.DockerContainerPrefix+env.server.UUIDShort())
	if err != nil {
		log.WithError(err).WithField("server", env.server.ID).Error("Failed to create docker container")
		return err
	}

	env.server.DockerContainer.ID = container.ID
	env.server.Save()

	log.WithField("server", env.server.ID).Debug("Docker environment created")
	return nil
}

// Destroy removes the environment's docker container
func (env *dockerEnvironment) Destroy() error {
	log.WithField("server", env.server.ID).Debug("Destroying docker environment")

	ctx := context.TODO()

	if err := env.inspectContainer(ctx); err != nil {
		log.WithError(err).Debug("Container not found error")
		log.WithField("server", env.server.ID).Debug("Container not found, docker environment destroyed already.")
		return nil
	}

	if err := env.client.ContainerRemove(ctx, env.server.DockerContainer.ID, types.ContainerRemoveOptions{}); err != nil {
		log.WithError(err).WithField("server", env.server.ID).Error("Failed to destroy docker environment")
		return err
	}

	log.WithField("server", env.server.ID).Debug("Docker environment destroyed")
	return nil
}

func (env *dockerEnvironment) Exists() bool {
	if err := env.inspectContainer(context.TODO()); err != nil {
		return false
	}
	return true
}

// Start starts the environment's docker container
func (env *dockerEnvironment) Start() error {
	log.WithField("server", env.server.ID).Debug("Starting service in docker environment")
	if err := env.attach(); err != nil {
		log.WithError(err).Error("Failed to attach to docker container")
	}

	if err := env.client.ContainerStart(context.TODO(), env.server.DockerContainer.ID, types.ContainerStartOptions{}); err != nil {
		log.WithError(err).Error("Failed to start docker container")
		return err
	}
	return nil
}

// Stop stops the environment's docker container
func (env *dockerEnvironment) Stop() error {
	log.WithField("server", env.server.ID).Debug("Stopping service in docker environment")

	// TODO: Decide after what timeout to kill the container, currently 30 seconds
	timeout := 30 * time.Second
	if err := env.client.ContainerStop(context.TODO(), env.server.DockerContainer.ID, &timeout); err != nil {
		log.WithError(err).Error("Failed to stop docker container")
		return err
	}
	return nil
}

func (env *dockerEnvironment) Kill() error {
	log.WithField("server", env.server.ID).Debug("Killing service in docker environment")

	if err := env.client.ContainerKill(context.TODO(), env.server.DockerContainer.ID, "KILL"); err != nil {
		log.WithError(err).Error("Failed to kill docker container")
		return err
	}
	return nil
}

// Exec sends commands to the standard input of the docker container
func (env *dockerEnvironment) Exec(command string) error {
	log.Debug("Command: " + command)
	_, err := env.hires.Conn.Write([]byte(command + "\n"))
	return err
}

func (env *dockerEnvironment) pullImage(ctx context.Context) error {
	// Split image repository and tag
	//imageParts := strings.Split(env.server.GetService().DockerImage, ":")
	//imageRepoParts := strings.Split(imageParts[0], "/")
	//if len(imageRepoParts) >= 3 {
	// TODO: Handle possibly required authentication
	//}

	// Pull docker image
	log.WithField("image", env.server.GetService().DockerImage).Debug("Pulling docker image")

	rc, err := env.client.ImagePull(ctx, env.server.GetService().DockerImage, types.ImagePullOptions{})
	if err != nil {
		return err
	}
	defer rc.Close()
	return nil
}
