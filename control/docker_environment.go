package control

import (
	"context"
	"strings"

	"github.com/fsouza/go-dockerclient"
	log "github.com/sirupsen/logrus"
)

type dockerEnvironment struct {
	baseEnvironment

	client    *docker.Client
	container *docker.Container
	context   context.Context

	server *server
}

// Ensure DockerEnvironment implements Environment
var _ Environment = &dockerEnvironment{}

// NewDockerEnvironment creates a new docker enviornment
// instance and connects to the docker client on the host system
// If the container is already running it will try to reattach
// to the running container
func NewDockerEnvironment(server *server) (Environment, error) {
	env := dockerEnvironment{}

	env.server = server

	client, err := docker.NewClient("unix:///var/run/docker.sock")
	env.client = client
	if err != nil {
		log.WithError(err).Fatal("Failed to connect to docker.")
		return nil, err
	}

	if env.server.DockerContainer.ID != "" {
		if err := env.reattach(); err != nil {
			log.WithError(err).Error("Failed to reattach to existing container.")
			return nil, err
		}
	}

	return &env, nil
}

func (env *dockerEnvironment) reattach() error {
	container, err := env.client.InspectContainer(env.server.DockerContainer.ID)
	if err != nil {
		return err
	}
	env.container = container
	return nil
}

// Create creates the docker container for the environment and applies all
// settings to it
func (env *dockerEnvironment) Create() error {
	log.WithField("serverID", env.server.UUID).Debug("Creating docker environment")
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
		log.WithError(err).WithField("serverID", env.server.UUID).Error("Failed to create docker environment")
		return err
	}

	// Create docker container
	// TODO: apply cpu, io, disk limits.
	containerConfig := &docker.Config{
		Image: env.server.Service().DockerImage,
	}
	containerHostConfig := &docker.HostConfig{
		Memory:     env.server.Settings.Memory,
		MemorySwap: env.server.Settings.Swap,
	}
	createContainerOpts := docker.CreateContainerOptions{
		Name:       "ptdl_" + env.server.UUIDShort(),
		Config:     containerConfig,
		HostConfig: containerHostConfig,
		Context:    env.context,
	}
	container, err := env.client.CreateContainer(createContainerOpts)
	if err != nil {
		log.WithError(err).WithField("serverID", env.server.UUID).Error("Failed to create docker container")
		return err
	}
	env.server.DockerContainer.ID = container.ID
	env.container = container

	return nil
}

// Destroy removes the environment's docker container
func (env *dockerEnvironment) Destroy() error {
	log.WithField("serverID", env.server.UUID).Debug("Destroying docker environment")
	err := env.client.RemoveContainer(docker.RemoveContainerOptions{
		ID: env.server.DockerContainer.ID,
	})
	if err != nil {
		log.WithError(err).WithField("serverID", env.server.UUID).Error("Failed to destroy docker environment")
		return err
	}
	return nil
}

// Start starts the environment's docker container
func (env *dockerEnvironment) Start() error {
	log.WithField("serverID", env.server.UUID).Debug("Starting service in docker environment")
	if err := env.client.StartContainer(env.container.ID, nil); err != nil {
		log.WithError(err).Error("Failed to start docker container")
		return err
	}
	return nil
}

// Stop stops the environment's docker container
func (env *dockerEnvironment) Stop() error {
	log.WithField("serverID", env.server.UUID).Debug("Stopping service in docker environment")
	if err := env.client.StopContainer(env.container.ID, 20000); err != nil {
		log.WithError(err).Error("Failed to stop docker container")
		return err
	}
	return nil
}

func (env *dockerEnvironment) Kill() error {
	log.WithField("serverID", env.server.UUID).Debug("Killing service in docker environment")
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
	return nil
}
