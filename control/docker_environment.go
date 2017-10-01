package control

import (
	"context"
	"io"
	"os"
	"strings"

	"github.com/Pterodactyl/wings/constants"

	"github.com/fsouza/go-dockerclient"
	log "github.com/sirupsen/logrus"
)

type dockerEnvironment struct {
	baseEnvironment

	client    *docker.Client
	container *docker.Container
	context   context.Context

	containerInput  io.Writer
	containerOutput io.Writer
	closeWaiter     docker.CloseWaiter

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

	client, err := docker.NewClient("unix:///var/run/docker.sock")
	env.client = client
	if err != nil {
		log.WithError(err).Fatal("Failed to connect to docker.")
		return nil, err
	}

	if env.server.DockerContainer.ID != "" {
		if err := env.checkContainerExists(); err != nil {
			log.WithError(err).Error("Failed to find the container with stored id, removing id.")
			env.server.DockerContainer.ID = ""
			env.server.Save()
		}
	}

	return &env, nil
}

func (env *dockerEnvironment) checkContainerExists() error {
	container, err := env.client.InspectContainer(env.server.DockerContainer.ID)
	if err != nil {
		return err
	}
	env.container = container
	return nil
}

func (env *dockerEnvironment) attach() error {
	pr, pw := io.Pipe()

	success := make(chan struct{})
	w, err := env.client.AttachToContainerNonBlocking(docker.AttachToContainerOptions{
		Container:    env.server.DockerContainer.ID,
		InputStream:  pr,
		OutputStream: os.Stdout,
		Stdin:        true,
		Stdout:       true,
		Stream:       true,
		Success:      success,
	})
	env.closeWaiter = w
	env.containerInput = pw

	<-success
	close(success)
	return err
}

// Create creates the docker container for the environment and applies all
// settings to it
func (env *dockerEnvironment) Create() error {
	log.WithField("server", env.server.ID).Debug("Creating docker environment")
	// Split image repository and tag to feed it to the library
	imageParts := strings.Split(env.server.Service().DockerImage, ":")
	imageRepoParts := strings.Split(imageParts[0], "/")
	if len(imageRepoParts) >= 3 {
		// Handle possibly required authentication
	}

	// Pull docker image
	var pullImageOpts = docker.PullImageOptions{
		Repository: imageParts[0],
	}
	if len(imageParts) >= 2 {
		pullImageOpts.Tag = imageParts[1]
	}
	log.WithField("image", env.server.service.DockerImage).Debug("Pulling docker image")
	err := env.client.PullImage(pullImageOpts, docker.AuthConfiguration{})
	if err != nil {
		log.WithError(err).WithField("server", env.server.ID).Error("Failed to create docker environment")
		return err
	}

	if err := os.MkdirAll(env.server.dataPath(), constants.DefaultFolderPerms); err != nil {
		return err
	}

	// Create docker container
	// TODO: apply cpu, io, disk limits.
	containerConfig := &docker.Config{
		Image:     env.server.Service().DockerImage,
		Cmd:       strings.Split(env.server.StartupCommand, " "),
		OpenStdin: true,
	}
	containerHostConfig := &docker.HostConfig{
		Memory:     env.server.Settings.Memory,
		MemorySwap: env.server.Settings.Swap,
		Binds:      []string{env.server.dataPath() + ":/home/container"},
	}
	createContainerOpts := docker.CreateContainerOptions{
		Name:       "ptdl-" + env.server.UUIDShort(),
		Config:     containerConfig,
		HostConfig: containerHostConfig,
		Context:    env.context,
	}
	container, err := env.client.CreateContainer(createContainerOpts)
	if err != nil {
		log.WithError(err).WithField("server", env.server.ID).Error("Failed to create docker container")
		return err
	}
	env.server.DockerContainer.ID = container.ID
	env.server.Save()
	env.container = container

	log.WithField("server", env.server.ID).Debug("Docker environment created")

	return nil
}

// Destroy removes the environment's docker container
func (env *dockerEnvironment) Destroy() error {
	log.WithField("server", env.server.ID).Debug("Destroying docker environment")
	if _, err := env.client.InspectContainer(env.server.DockerContainer.ID); err != nil {
		if _, ok := err.(*docker.NoSuchContainer); ok {
			log.WithField("server", env.server.ID).Debug("Container not found, docker environment destroyed already.")
			return nil
		}
		log.WithError(err).WithField("server", env.server.ID).Error("Could not destroy docker environment")
		return err
	}
	err := env.client.RemoveContainer(docker.RemoveContainerOptions{
		ID: env.server.DockerContainer.ID,
	})
	if err != nil {
		log.WithError(err).WithField("server", env.server.ID).Error("Failed to destroy docker environment")
		return err
	}

	log.WithField("server", env.server.ID).Debug("Docker environment destroyed")
	return nil
}

func (env *dockerEnvironment) Exists() bool {
	if env.container != nil {
		return true
	}
	env.checkContainerExists()
	return env.container != nil
}

// Start starts the environment's docker container
func (env *dockerEnvironment) Start() error {
	log.WithField("server", env.server.ID).Debug("Starting service in docker environment")
	if err := env.attach(); err != nil {
		log.WithError(err).Error("Failed to attach to docker container")
	}
	if err := env.client.StartContainer(env.container.ID, nil); err != nil {
		log.WithError(err).Error("Failed to start docker container")
		return err
	}
	return nil
}

// Stop stops the environment's docker container
func (env *dockerEnvironment) Stop() error {
	log.WithField("server", env.server.ID).Debug("Stopping service in docker environment")
	if err := env.client.StopContainer(env.container.ID, 20000); err != nil {
		log.WithError(err).Error("Failed to stop docker container")
		return err
	}
	return nil
}

func (env *dockerEnvironment) Kill() error {
	log.WithField("server", env.server.ID).Debug("Killing service in docker environment")
	if err := env.client.KillContainer(docker.KillContainerOptions{
		ID: env.container.ID,
	}); err != nil {
		log.WithError(err).Error("Failed to kill docker container")
		return err
	}
	return nil
}

// Exec sends commands to the standard input of the docker container
func (env *dockerEnvironment) Exec(command string) error {
	log.Debug("Command: " + command)
	_, err := env.containerInput.Write([]byte(command + "\n"))
	return err
}
