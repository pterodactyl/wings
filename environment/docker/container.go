package docker

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"github.com/apex/log"
	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/client"
	"github.com/docker/docker/daemon/logger/jsonfilelog"
	"github.com/pkg/errors"
	"github.com/pterodactyl/wings/config"
	"github.com/pterodactyl/wings/environment"
	"io"
	"strconv"
	"strings"
	"time"
)

type imagePullStatus struct {
	Status   string `json:"status"`
	Progress string `json:"progress"`
}

// Attaches to the docker container itself and ensures that we can pipe data in and out
// of the process stream. This should not be used for reading console data as you *will*
// miss important output at the beginning because of the time delay with attaching to the
// output.
func (e *Environment) Attach() error {
	if e.IsAttached() {
		return nil
	}

	if err := e.followOutput(); err != nil {
		return errors.WithStack(err)
	}

	opts := types.ContainerAttachOptions{
		Stdin:  true,
		Stdout: true,
		Stderr: true,
		Stream: true,
	}

	// Set the stream again with the container.
	if st, err := e.client.ContainerAttach(context.Background(), e.Id, opts); err != nil {
		return errors.WithStack(err)
	} else {
		e.SetStream(&st)
	}

	c := new(Console)
	go func(console *Console) {
		ctx, cancel := context.WithCancel(context.Background())

		defer cancel()
		defer e.stream.Close()
		defer func() {
			e.setState(environment.ProcessOfflineState)
			e.SetStream(nil)
		}()

		// Poll resources in a separate thread since this will block the copy call below
		// from being reached until it is completed if not run in a separate process. However,
		// we still want it to be stopped when the copy operation below is finished running which
		// indicates that the container is no longer running.
		go func(ctx context.Context) {
			if err := e.pollResources(ctx); err != nil {
				log.WithField("environment_id", e.Id).WithField("error", errors.WithStack(err)).Error("error during environment resource polling")
			}
		}(ctx)

		// Stream the reader output to the console which will then fire off events and handle console
		// throttling and sending the output to the user.
		if _, err := io.Copy(console, e.stream.Reader); err != nil {
			log.WithField("environment_id", e.Id).WithField("error", errors.WithStack(err)).Error("error while copying environment output to console")
		}
	}(c)

	return nil
}

func (e *Environment) resources() container.Resources {
	l := e.Configuration.Limits()

	return container.Resources{
		Memory:            l.BoundedMemoryLimit(),
		MemoryReservation: l.MemoryLimit * 1_000_000,
		MemorySwap:        l.ConvertedSwap(),
		CPUQuota:          l.ConvertedCpuLimit(),
		CPUPeriod:         100_000,
		CPUShares:         1024,
		BlkioWeight:       l.IoWeight,
		OomKillDisable:    &l.OOMDisabled,
		CpusetCpus:        l.Threads,
	}
}

// Performs an in-place update of the Docker container's resource limits without actually
// making any changes to the operational state of the container. This allows memory, cpu,
// and IO limitations to be adjusted on the fly for individual instances.
func (e *Environment) InSituUpdate() error {
	if _, err := e.client.ContainerInspect(context.Background(), e.Id); err != nil {
		// If the container doesn't exist for some reason there really isn't anything
		// we can do to fix that in this process (it doesn't make sense at least). In those
		// cases just return without doing anything since we still want to save the configuration
		// to the disk.
		//
		// We'll let a boot process make modifications to the container if needed at this point.
		if client.IsErrNotFound(err) {
			return nil
		}

		return errors.WithStack(err)
	}

	u := container.UpdateConfig{
		Resources: e.resources(),
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second*10)
	defer cancel()
	if _, err := e.client.ContainerUpdate(ctx, e.Id, u); err != nil {
		return errors.WithStack(err)
	}

	return nil
}

// Creates a new container for the server using all of the data that is currently
// available for it. If the container already exists it will be returnee.
func (e *Environment) Create() error {
	// If the container already exists don't hit the user with an error, just return
	// the current information about it which is what we would do when creating the
	// container anyways.
	if _, err := e.client.ContainerInspect(context.Background(), e.Id); err == nil {
		return nil
	} else if !client.IsErrNotFound(err) {
		return errors.WithStack(err)
	}

	// Try to pull the requested image before creating the container.
	if err := e.ensureImageExists(e.meta.Image); err != nil {
		return errors.WithStack(err)
	}

	a := e.Configuration.Allocations()

	evs := e.Configuration.EnvironmentVariables()
	for i, v := range evs {
		// Convert 127.0.0.1 to the pterodactyl0 network interface if the environment is Docker
		// so that the server operates as expected.
		if v == "SERVER_IP=127.0.0.1" {
			evs[i] = "SERVER_IP=" + config.Get().Docker.Network.Interface
		}
	}

	conf := &container.Config{
		Hostname:     e.Id,
		Domainname:   config.Get().Docker.Domainname,
		User:         strconv.Itoa(config.Get().System.User.Uid),
		AttachStdin:  true,
		AttachStdout: true,
		AttachStderr: true,
		OpenStdin:    true,
		Tty:          true,
		ExposedPorts: a.Exposed(),
		Image:        e.meta.Image,
		Env:          e.Configuration.EnvironmentVariables(),
		Labels: map[string]string{
			"Service":       "Pterodactyl",
			"ContainerType": "server_process",
		},
	}

	tmpfsSize := strconv.Itoa(int(config.Get().Docker.TmpfsSize))

	hostConf := &container.HostConfig{
		PortBindings: a.DockerBindings(),

		// Configure the mounts for this container. First mount the server data directory
		// into the container as a r/w bind.
		Mounts: e.convertMounts(),

		// Configure the /tmp folder mapping in containers. This is necessary for some
		// games that need to make use of it for downloads and other installation processes.
		Tmpfs: map[string]string{
			"/tmp": "rw,exec,nosuid,size=" + tmpfsSize + "M",
		},

		// Define resource limits for the container based on the data passed through
		// from the Panel.
		Resources: e.resources(),

		DNS: config.Get().Docker.Network.Dns,

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
		NetworkMode: container.NetworkMode(config.Get().Docker.Network.Mode),
	}

	if _, err := e.client.ContainerCreate(context.Background(), conf, hostConf, nil, e.Id); err != nil {
		return errors.WithStack(err)
	}

	return nil
}

func (e *Environment) convertMounts() []mount.Mount {
	var out []mount.Mount

	for _, m := range e.Configuration.Mounts() {
		out = append(out, mount.Mount{
			Type:     mount.TypeBind,
			Source:   m.Source,
			Target:   m.Target,
			ReadOnly: m.ReadOnly,
		})
	}

	return out
}

// Remove the Docker container from the machine. If the container is currently running
// it will be forcibly stopped by Docker.
func (e *Environment) Destroy() error {
	// We set it to stopping than offline to prevent crash detection from being triggered.
	e.setState(environment.ProcessStoppingState)

	err := e.client.ContainerRemove(context.Background(), e.Id, types.ContainerRemoveOptions{
		RemoveVolumes: true,
		RemoveLinks:   false,
		Force:         true,
	})

	// Don't trigger a destroy failure if we try to delete a container that does not
	// exist on the system. We're just a step ahead of ourselves in that case.
	//
	// @see https://github.com/pterodactyl/panel/issues/2001
	if err != nil && client.IsErrNotFound(err) {
		return nil
	}

	e.setState(environment.ProcessOfflineState)

	return err
}

// Attaches to the log for the container. This avoids us missing crucial output that
// happens in the split seconds before the code moves from 'Starting' to 'Attaching'
// on the process.
func (e *Environment) followOutput() error {
	if exists, err := e.Exists(); !exists {
		if err != nil {
			return errors.WithStack(err)
		}

		return errors.New(fmt.Sprintf("no such container: %s", e.Id))
	}

	opts := types.ContainerLogsOptions{
		ShowStderr: true,
		ShowStdout: true,
		Follow:     true,
		Since:      time.Now().Format(time.RFC3339),
	}

	reader, err := e.client.ContainerLogs(context.Background(), e.Id, opts)

	go func(r io.ReadCloser) {
		defer r.Close()

		s := bufio.NewScanner(r)
		for s.Scan() {
			e.Events().Publish(environment.ConsoleOutputEvent, s.Text())
		}

		if err := s.Err(); err != nil {
			log.WithField("error", err).WithField("container_id", e.Id).Warn("error processing scanner line in console output")
		}
	}(reader)

	return errors.WithStack(err)
}

// Pulls the image from Docker. If there is an error while pulling the image from the source
// but the image already exists locally, we will report that error to the logger but continue
// with the process.
//
// The reasoning behind this is that Quay has had some serious outages as of late, and we don't
// need to block all of the servers from booting just because of that. I'd imagine in a lot of
// cases an outage shouldn't affect users too badly. It'll at least keep existing servers working
// correctly if anything.
//
// TODO: local images
func (e *Environment) ensureImageExists(image string) error {
	e.Events().Publish(environment.DockerImagePullStarted, "")
	defer e.Events().Publish(environment.DockerImagePullCompleted, "")

	// Give it up to 15 minutes to pull the image. I think this should cover 99.8% of cases where an
	// image pull might fail. I can't imagine it will ever take more than 15 minutes to fully pull
	// an image. Let me know when I am inevitably wrong here...
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute*15)
	defer cancel()

	// Get a registry auth configuration from the config.
	var registryAuth *config.RegistryConfiguration
	for registry, c := range config.Get().Docker.Registries {
		if !strings.HasPrefix(image, registry) {
			continue
		}

		log.WithField("registry", registry).Debug("using authentication for registry")
		registryAuth = &c
		break
	}

	// Get the ImagePullOptions.
	imagePullOptions := types.ImagePullOptions{All: false}
	if registryAuth != nil {
		b64, err := registryAuth.Base64()
		if err != nil {
			log.WithError(err).Error("failed to get registry auth credentials")
		}

		// b64 is a string so if there is an error it will just be empty, not nil.
		imagePullOptions.RegistryAuth = b64
	}

	out, err := e.client.ImagePull(ctx, image, imagePullOptions)
	if err != nil {
		images, ierr := e.client.ImageList(ctx, types.ImageListOptions{})
		if ierr != nil {
			// Well damn, something has gone really wrong here, just go ahead and abort there
			// isn't much anything we can do to try and self-recover from this.
			return ierr
		}

		for _, img := range images {
			for _, t := range img.RepoTags {
				if t != image {
					continue
				}

				log.WithFields(log.Fields{
					"image":        image,
					"container_id": e.Id,
					"err":          err.Error(),
				}).Warn("unable to pull requested image from remote source, however the image exists locally")

				// Okay, we found a matching container image, in that case just go ahead and return
				// from this function, since there is nothing else we need to do here.
				return nil
			}
		}

		return err
	}
	defer out.Close()

	log.WithField("image", image).Debug("pulling docker image... this could take a bit of time")

	// I'm not sure what the best approach here is, but this will block execution until the image
	// is done being pulled, which is what we need.
	scanner := bufio.NewScanner(out)
	for scanner.Scan() {
		s := imagePullStatus{}
		fmt.Println(scanner.Text())
		if err := json.Unmarshal(scanner.Bytes(), &s); err == nil {
			e.Events().Publish(environment.DockerImagePullStatus, s.Status+" "+s.Progress)
		}
	}

	if err := scanner.Err(); err != nil {
		return err
	}

	log.WithField("image", image).Debug("completed docker image pull")

	return nil
}
