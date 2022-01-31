package docker

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"emperror.dev/errors"
	"github.com/apex/log"
	"github.com/buger/jsonparser"
	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/client"
	"github.com/docker/docker/daemon/logger/local"

	"github.com/pterodactyl/wings/config"
	"github.com/pterodactyl/wings/environment"
	"github.com/pterodactyl/wings/system"
)

var ErrNotAttached = errors.Sentinel("not attached to instance")

// A custom console writer that allows us to keep a function blocked until the
// given stream is properly closed. This does nothing special, only exists to
// make a noop io.Writer.
type noopWriter struct{}

var _ io.Writer = noopWriter{}

// Implement the required Write function to satisfy the io.Writer interface.
func (nw noopWriter) Write(b []byte) (int, error) {
	return len(b), nil
}

// Attach attaches to the docker container itself and ensures that we can pipe
// data in and out of the process stream. This should always be called before
// you have started the container, but after you've ensured it exists.
//
// Calling this function will poll resources for the container in the background
// until the container is stopped. The context provided to this function is used
// for the purposes of attaching to the container, a seecond context is created
// within the function for managing polling.
func (e *Environment) Attach(ctx context.Context) error {
	if e.IsAttached() {
		return nil
	}

	if err := e.followOutput(); err != nil {
		return err
	}

	opts := types.ContainerAttachOptions{
		Stdin:  true,
		Stdout: true,
		Stderr: true,
		Stream: true,
	}

	// Set the stream again with the container.
	if st, err := e.client.ContainerAttach(ctx, e.Id, opts); err != nil {
		return err
	} else {
		e.SetStream(&st)
	}

	go func() {
		// Don't use the context provided to the function, that'll cause the polling to
		// exit unexpectedly. We want a custom context for this, the one passed to the
		// function is to avoid a hang situation when trying to attach to a container.
		pollCtx, cancel := context.WithCancel(context.Background())
		defer cancel()
		defer e.stream.Close()
		defer func() {
			e.SetState(environment.ProcessOfflineState)
			e.SetStream(nil)
		}()

		go func() {
			if err := e.pollResources(pollCtx); err != nil {
				if !errors.Is(err, context.Canceled) {
					e.log().WithField("error", err).Error("error during environment resource polling")
				} else {
					e.log().Warn("stopping server resource polling: context canceled")
				}
			}
		}()

		// Block the completion of this routine until the container is no longer running. This allows
		// the pollResources function to run until it needs to be stopped. Because the container
		// can be polled for resource usage, even when stopped, we need to have this logic present
		// in order to cancel the context and therefore stop the routine that is spawned.
		//
		// For now, DO NOT use client#ContainerWait from the Docker package. There is a nasty
		// bug causing containers to hang on deletion and cause servers to lock up on the system.
		//
		// This weird code isn't intuitive, but it keeps the function from ending until the container
		// is stopped and therefore the stream reader ends up closed.
		// @see https://github.com/moby/moby/issues/41827
		c := new(noopWriter)
		if _, err := io.Copy(c, e.stream.Reader); err != nil {
			e.log().WithField("error", err).Error("could not copy from environment stream to noop writer")
		}
	}()

	return nil
}

// InSituUpdate performs an in-place update of the Docker container's resource
// limits without actually making any changes to the operational state of the
// container. This allows memory, cpu, and IO limitations to be adjusted on the
// fly for individual instances.
func (e *Environment) InSituUpdate() error {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*10)
	defer cancel()

	if _, err := e.ContainerInspect(ctx); err != nil {
		// If the container doesn't exist for some reason there really isn't anything
		// we can do to fix that in this process (it doesn't make sense at least). In those
		// cases just return without doing anything since we still want to save the configuration
		// to the disk.
		//
		// We'll let a boot process make modifications to the container if needed at this point.
		if client.IsErrNotFound(err) {
			return nil
		}
		return errors.Wrap(err, "environment/docker: could not inspect container")
	}

	// CPU pinning cannot be removed once it is applied to a container. The same is true
	// for removing memory limits, a container must be re-created.
	//
	// @see https://github.com/moby/moby/issues/41946
	if _, err := e.client.ContainerUpdate(ctx, e.Id, container.UpdateConfig{
		Resources: e.Configuration.Limits().AsContainerResources(),
	}); err != nil {
		return errors.Wrap(err, "environment/docker: could not update container")
	}
	return nil
}

// Create creates a new container for the server using all the data that is
// currently available for it. If the container already exists it will be
// returned.
func (e *Environment) Create() error {
	// If the container already exists don't hit the user with an error, just return
	// the current information about it which is what we would do when creating the
	// container anyways.
	if _, err := e.ContainerInspect(context.Background()); err == nil {
		return nil
	} else if !client.IsErrNotFound(err) {
		return errors.Wrap(err, "environment/docker: failed to inspect container")
	}

	// Try to pull the requested image before creating the container.
	if err := e.ensureImageExists(e.meta.Image); err != nil {
		return errors.WithStackIf(err)
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
		User:         strconv.Itoa(config.Get().System.User.Uid) + ":" + strconv.Itoa(config.Get().System.User.Gid),
		AttachStdin:  true,
		AttachStdout: true,
		AttachStderr: true,
		OpenStdin:    true,
		Tty:          true,
		ExposedPorts: a.Exposed(),
		Image:        strings.TrimPrefix(e.meta.Image, "~"),
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
		// into the container as an r/w bind.
		Mounts: e.convertMounts(),

		// Configure the /tmp folder mapping in containers. This is necessary for some
		// games that need to make use of it for downloads and other installation processes.
		Tmpfs: map[string]string{
			"/tmp": "rw,exec,nosuid,size=" + tmpfsSize + "M",
		},

		// Define resource limits for the container based on the data passed through
		// from the Panel.
		Resources: e.Configuration.Limits().AsContainerResources(),

		DNS: config.Get().Docker.Network.Dns,

		// Configure logging for the container to make it easier on the Daemon to grab
		// the server output. Ensure that we don't use too much space on the host machine
		// since we only need it for the last few hundred lines of output and don't care
		// about anything else in it.
		LogConfig: container.LogConfig{
			Type: local.Name,
			Config: map[string]string{
				"max-size": "5m",
				"max-file": "1",
				"compress": "false",
				"mode":     "non-blocking",
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

	if _, err := e.client.ContainerCreate(context.Background(), conf, hostConf, nil, nil, e.Id); err != nil {
		return errors.Wrap(err, "environment/docker: failed to create container")
	}

	return nil
}

// Destroy will remove the Docker container from the server. If the container
// is currently running it will be forcibly stopped by Docker.
func (e *Environment) Destroy() error {
	// We set it to stopping than offline to prevent crash detection from being triggered.
	e.SetState(environment.ProcessStoppingState)

	err := e.client.ContainerRemove(context.Background(), e.Id, types.ContainerRemoveOptions{
		RemoveVolumes: true,
		RemoveLinks:   false,
		Force:         true,
	})

	e.SetState(environment.ProcessOfflineState)

	// Don't trigger a destroy failure if we try to delete a container that does not
	// exist on the system. We're just a step ahead of ourselves in that case.
	//
	// @see https://github.com/pterodactyl/panel/issues/2001
	if err != nil && client.IsErrNotFound(err) {
		return nil
	}

	return err
}

// SendCommand sends the specified command to the stdin of the running container
// instance. There is no confirmation that this data is sent successfully, only
// that it gets pushed into the stdin.
func (e *Environment) SendCommand(c string) error {
	if !e.IsAttached() {
		return errors.Wrap(ErrNotAttached, "environment/docker: cannot send command to container")
	}

	e.mu.RLock()
	defer e.mu.RUnlock()

	// If the command being processed is the same as the process stop command then we
	// want to mark the server as entering the stopping state otherwise the process will
	// stop and Wings will think it has crashed and attempt to restart it.
	if e.meta.Stop.Type == "command" && c == e.meta.Stop.Value {
		e.SetState(environment.ProcessStoppingState)
	}

	_, err := e.stream.Conn.Write([]byte(c + "\n"))

	return errors.Wrap(err, "environment/docker: could not write to container stream")
}

// Readlog reads the log file for the server. This does not care if the server
// is running or not, it will simply try to read the last X bytes of the file
// and return them.
func (e *Environment) Readlog(lines int) ([]string, error) {
	r, err := e.client.ContainerLogs(context.Background(), e.Id, types.ContainerLogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Tail:       strconv.Itoa(lines),
	})
	if err != nil {
		return nil, errors.WithStack(err)
	}
	defer r.Close()

	var out []string
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		out = append(out, scanner.Text())
	}

	return out, nil
}

// Attaches to the log for the container. This avoids us missing crucial output
// that happens in the split seconds before the code moves from 'Starting' to
// 'Attaching' on the process.
func (e *Environment) followOutput() error {
	if exists, err := e.Exists(); !exists {
		if err != nil {
			return err
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
	if err != nil {
		return err
	}

	go e.scanOutput(reader)

	return nil
}

func (e *Environment) scanOutput(reader io.ReadCloser) {
	defer reader.Close()

	if err := system.ScanReader(reader, func(v []byte) {
		e.logCallbackMx.Lock()
		defer e.logCallbackMx.Unlock()
		e.logCallback(v)
	}); err != nil && err != io.EOF {
		log.WithField("error", err).WithField("container_id", e.Id).Warn("error processing scanner line in console output")
		return
	}

	// Return here if the server is offline or currently stopping.
	if e.State() == environment.ProcessStoppingState || e.State() == environment.ProcessOfflineState {
		return
	}

	// Close the current reader before starting a new one, the defer will still run,
	// but it will do nothing if we already closed the stream.
	_ = reader.Close()

	// Start following the output of the server again.
	go e.followOutput()
}

// Pulls the image from Docker. If there is an error while pulling the image
// from the source but the image already exists locally, we will report that
// error to the logger but continue with the process.
//
// The reasoning behind this is that Quay has had some serious outages as of
// late, and we don't need to block all the servers from booting just because
// of that. I'd imagine in a lot of cases an outage shouldn't affect users too
// badly. It'll at least keep existing servers working correctly if anything.
func (e *Environment) ensureImageExists(image string) error {
	e.Events().Publish(environment.DockerImagePullStarted, "")
	defer e.Events().Publish(environment.DockerImagePullCompleted, "")

	// Images prefixed with a ~ are local images that we do not need to try and pull.
	if strings.HasPrefix(image, "~") {
		return nil
	}

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
			return errors.Wrap(ierr, "environment/docker: failed to list images")
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

		return errors.Wrapf(err, "environment/docker: failed to pull \"%s\" image for server", image)
	}
	defer out.Close()

	log.WithField("image", image).Debug("pulling docker image... this could take a bit of time")

	// I'm not sure what the best approach here is, but this will block execution until the image
	// is done being pulled, which is what we need.
	scanner := bufio.NewScanner(out)

	for scanner.Scan() {
		b := scanner.Bytes()
		status, _ := jsonparser.GetString(b, "status")
		progress, _ := jsonparser.GetString(b, "progress")

		e.Events().Publish(environment.DockerImagePullStatus, status+" "+progress)
	}

	if err := scanner.Err(); err != nil {
		return err
	}

	log.WithField("image", image).Debug("completed docker image pull")

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

func (e *Environment) resources() container.Resources {
	l := e.Configuration.Limits()
	pids := l.ProcessLimit()

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
		PidsLimit:         &pids,
	}
}
