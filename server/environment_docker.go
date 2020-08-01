package server

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/apex/log"
	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/client"
	"github.com/docker/docker/daemon/logger/jsonfilelog"
	"github.com/docker/go-connections/nat"
	"github.com/pkg/errors"
	"github.com/pterodactyl/wings/api"
	"github.com/pterodactyl/wings/config"
	"golang.org/x/sync/semaphore"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Defines the base environment for Docker instances running through Wings.
type DockerEnvironment struct {
	sync.RWMutex

	Server *Server

	// The Docker client being used for this instance.
	Client *client.Client

	// Tracks if we are currently attached to the server container. This allows us to attach
	// once and then just use that attachment to stream logs out of the server and also stream
	// commands back into it without constantly attaching and detaching.
	attached bool

	// Controls the hijacked response stream which exists only when we're attached to
	// the running container instance.
	stream types.HijackedResponse

	// Holds the stats stream used by the polling commands so that we can easily close
	// it out.
	stats io.ReadCloser

	// Locks when we're performing a restart to avoid trying to restart a process that is already
	// being restarted.
	restartSem *semaphore.Weighted
}

// Set if this process is currently attached to the process.
func (d *DockerEnvironment) SetAttached(a bool) {
	d.Lock()
	d.attached = a
	d.Unlock()
}

// Determine if the this process is currently attached to the container.
func (d *DockerEnvironment) IsAttached() bool {
	d.RLock()
	defer d.RUnlock()

	return d.attached
}

// Creates a new base Docker environment. A server must still be attached to it.
func NewDockerEnvironment(server *Server) error {
	cli, err := client.NewClientWithOpts(client.FromEnv)
	if err != nil {
		return err
	}

	server.Environment = &DockerEnvironment{
		Server: server,
		Client: cli,
	}

	return nil
}

// Ensure that the Docker environment is always implementing all of the methods
// from the base environment interface.
var _ Environment = (*DockerEnvironment)(nil)

// Returns the name of the environment.
func (d *DockerEnvironment) Type() string {
	return "docker"
}

// Determines if the container exists in this environment.
func (d *DockerEnvironment) Exists() (bool, error) {
	_, err := d.Client.ContainerInspect(context.Background(), d.Server.Id())

	if err != nil {
		// If this error is because the container instance wasn't found via Docker we
		// can safely ignore the error and just return false.
		if client.IsErrNotFound(err) {
			return false, nil
		}

		return false, err
	}

	return true, nil
}

// Determines if the server's docker container is currently running. If there is no container
// present, an error will be raised (since this shouldn't be a case that ever happens under
// correctly developed circumstances).
//
// You can confirm if the instance wasn't found by using client.IsErrNotFound from the Docker
// API.
//
// @see docker/client/errors.go
func (d *DockerEnvironment) IsRunning() (bool, error) {
	c, err := d.Client.ContainerInspect(context.Background(), d.Server.Id())
	if err != nil {
		return false, err
	}

	return c.State.Running, nil
}

// Performs an in-place update of the Docker container's resource limits without actually
// making any changes to the operational state of the container. This allows memory, cpu,
// and IO limitations to be adjusted on the fly for individual instances.
func (d *DockerEnvironment) InSituUpdate() error {
	if _, err := d.Client.ContainerInspect(context.Background(), d.Server.Id()); err != nil {
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
		Resources: d.getResourcesForServer(),
	}

	d.Server.Log().WithField("limits", fmt.Sprintf("%+v", u.Resources)).Debug("updating server container on-the-fly with passed limits")

	ctx, cancel := context.WithTimeout(context.Background(), time.Second*10)
	defer cancel()
	if _, err := d.Client.ContainerUpdate(ctx, d.Server.Id(), u); err != nil {
		return errors.WithStack(err)
	}

	return nil
}

// Run before the container starts and get the process configuration from the Panel.
// This is important since we use this to check configuration files as well as ensure
// we always have the latest version of an egg available for server processes.
//
// This process will also confirm that the server environment exists and is in a bootable
// state. This ensures that unexpected container deletion while Wings is running does
// not result in the server becoming unbootable.
func (d *DockerEnvironment) OnBeforeStart() error {
	d.Server.Log().Info("syncing server configuration with panel")
	if err := d.Server.Sync(); err != nil {
		return err
	}

	if !d.Server.Filesystem.HasSpaceAvailable() {
		return errors.New("cannot start server, not enough disk space available")
	}

	// Always destroy and re-create the server container to ensure that synced data from
	// the Panel is used.
	if err := d.Client.ContainerRemove(context.Background(), d.Server.Id(), types.ContainerRemoveOptions{RemoveVolumes: true}); err != nil {
		if !client.IsErrNotFound(err) {
			return err
		}
	}

	// The Create() function will check if the container exists in the first place, and if
	// so just silently return without an error. Otherwise, it will try to create the necessary
	// container and data storage directory.
	//
	// This won't actually run an installation process however, it is just here to ensure the
	// environment gets created properly if it is missing and the server is started. We're making
	// an assumption that all of the files will still exist at this point.
	if err := d.Create(); err != nil {
		return err
	}

	return nil
}

// Starts the server environment and begins piping output to the event listeners for the
// console. If a container does not exist, or needs to be rebuilt that will happen in the
// call to OnBeforeStart().
func (d *DockerEnvironment) Start() error {
	sawError := false
	// If sawError is set to true there was an error somewhere in the pipeline that
	// got passed up, but we also want to ensure we set the server to be offline at
	// that point.
	defer func() {
		if sawError {
			// If we don't set it to stopping first, you'll trigger crash detection which
			// we don't want to do at this point since it'll just immediately try to do the
			// exact same action that lead to it crashing in the first place...
			d.Server.SetState(ProcessStoppingState)
			d.Server.SetState(ProcessOfflineState)
		}
	}()

	// If the server is suspended the user shouldn't be able to boot it, in those cases
	// return a suspension error and let the calling area handle the issue.
	//
	// Theoretically you'd have the Panel handle all of this logic, but we cannot do that
	// because we allow the websocket to control the server power state as well, so we'll
	// need to handle that action in here.
	if d.Server.IsSuspended() {
		return &suspendedError{}
	}

	if c, err := d.Client.ContainerInspect(context.Background(), d.Server.Id()); err != nil {
		// Do nothing if the container is not found, we just don't want to continue
		// to the next block of code here. This check was inlined here to guard againt
		// a nil-pointer when checking c.State below.
		//
		// @see https://github.com/pterodactyl/panel/issues/2000
		if !client.IsErrNotFound(err) {
			return errors.WithStack(err)
		}
	} else {
		// If the server is running update our internal state and continue on with the attach.
		if c.State.Running {
			d.Server.SetState(ProcessRunningState)

			return d.Attach()
		}

		// Truncate the log file so we don't end up outputting a bunch of useless log information
		// to the websocket and whatnot. Check first that the path and file exist before trying
		// to truncate them.
		if _, err := os.Stat(c.LogPath); err == nil {
			if err := os.Truncate(c.LogPath, 0); err != nil {
				return errors.WithStack(err)
			}
		}
	}

	d.Server.SetState(ProcessStartingState)
	// Set this to true for now, we will set it to false once we reach the
	// end of this chain.
	sawError = true

	// Run the before start function and wait for it to finish. This will validate that the container
	// exists on the system, and rebuild the container if that is required for server booting to
	// occur.
	if err := d.OnBeforeStart(); err != nil {
		return errors.WithStack(err)
	}

	// Update the configuration files defined for the server before beginning the boot process.
	// This process executes a bunch of parallel updates, so we just block until that process
	// is completed. Any errors as a result of this will just be bubbled out in the logger,
	// we don't need to actively do anything about it at this point, worst comes to worst the
	// server starts in a weird state and the user can manually adjust.
	d.Server.UpdateConfigurationFiles()

	// Reset the permissions on files for the server before actually trying
	// to start it.
	if err := d.Server.Filesystem.Chown("/"); err != nil {
		return errors.WithStack(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second*10)
	defer cancel()

	if err := d.Client.ContainerStart(ctx, d.Server.Id(), types.ContainerStartOptions{}); err != nil {
		return errors.WithStack(err)
	}

	// No errors, good to continue through.
	sawError = false

	return d.Attach()
}

// Stops the container that the server is running in. This will allow up to 10
// seconds to pass before a failure occurs.
func (d *DockerEnvironment) Stop() error {
	stop := d.Server.ProcessConfiguration().Stop
	if stop.Type == api.ProcessStopSignal {
		return d.Terminate(os.Kill)
	}

	d.Server.SetState(ProcessStoppingState)
	// Only attempt to send the stop command to the instance if we are actually attached to
	// the instance. If we are not for some reason, just send the container stop event.
	if d.IsAttached() && stop.Type == api.ProcessStopCommand {
		return d.SendCommand(stop.Value)
	}

	t := time.Second * 10

	err := d.Client.ContainerStop(context.Background(), d.Server.Id(), &t)
	if err != nil {
		// If the container does not exist just mark the process as stopped and return without
		// an error.
		if client.IsErrNotFound(err) {
			d.SetAttached(false)
			d.Server.SetState(ProcessOfflineState)

			return nil
		}

		return err
	}

	return nil
}

// Try to acquire a lock to restart the server. If one cannot be obtained within 5 seconds return
// an error to the caller. You should ideally be checking IsRestarting() before calling this function
// to avoid unnecessary delays since you can respond immediately from that.
func (d *DockerEnvironment) acquireRestartLock() error {
	if d.restartSem == nil {
		d.restartSem = semaphore.NewWeighted(1)
	}

	ctx, _ := context.WithTimeout(context.Background(), time.Second*5)

	return d.restartSem.Acquire(ctx, 1)
}

// Restarts the server process by waiting for the process to gracefully stop and then triggering a
// start command. This will return an error if there is already a restart process executing for the
// server. The lock is released when the process is stopped and a start has begun.
func (d *DockerEnvironment) Restart() error {
	d.Server.Log().Debug("acquiring process restart lock...")
	if err := d.acquireRestartLock(); err != nil {
		d.Server.Log().Warn("failed to acquire restart lock; already acquired by a different process")
		return err
	}

	d.Server.Log().Info("acquired process lock; beginning restart process...")

	err := d.WaitForStop(60, false)
	if err != nil {
		d.restartSem.Release(1)
		return err
	}

	// Release the restart lock, it is now safe for someone to attempt restarting the server again.
	d.restartSem.Release(1)

	// Start the process.
	return d.Start()
}

// Check if the server is currently running the restart process by checking if there is a semaphore
// allocated, and if so, if we can acquire a lock on it.
func (d *DockerEnvironment) IsRestarting() bool {
	if d.restartSem == nil {
		return false
	}

	if d.restartSem.TryAcquire(1) {
		d.restartSem.Release(1)

		return false
	}

	return true
}

// Attempts to gracefully stop a server using the defined stop command. If the server
// does not stop after seconds have passed, an error will be returned, or the instance
// will be terminated forcefully depending on the value of the second argument.
func (d *DockerEnvironment) WaitForStop(seconds int, terminate bool) error {
	if d.Server.GetState() == ProcessOfflineState {
		return nil
	}

	if err := d.Stop(); err != nil {
		return errors.WithStack(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(seconds)*time.Second)
	defer cancel()

	// Block the return of this function until the container as been marked as no
	// longer running. If this wait does not end by the time seconds have passed,
	// attempt to terminate the container, or return an error.
	ok, errChan := d.Client.ContainerWait(ctx, d.Server.Id(), container.WaitConditionNotRunning)
	select {
	case <-ctx.Done():
		if ctxErr := ctx.Err(); ctxErr != nil {
			if terminate {
				return d.Terminate(os.Kill)
			}

			return errors.WithStack(ctxErr)
		}
	case err := <-errChan:
		if err != nil {
			return errors.WithStack(err)
		}
	case <-ok:
	}

	return nil
}

// Forcefully terminates the container using the signal passed through.
func (d *DockerEnvironment) Terminate(signal os.Signal) error {
	c, err := d.Client.ContainerInspect(context.Background(), d.Server.Id())
	if err != nil {
		return errors.WithStack(err)
	}

	if !c.State.Running {
		return nil
	}

	d.Server.SetState(ProcessStoppingState)

	return d.Client.ContainerKill(
		context.Background(), d.Server.Id(), strings.TrimSuffix(strings.TrimPrefix(signal.String(), "signal "), "ed"),
	)
}

// Remove the Docker container from the machine. If the container is currently running
// it will be forcibly stopped by Docker.
func (d *DockerEnvironment) Destroy() error {
	// Avoid crash detection firing off.
	d.Server.SetState(ProcessStoppingState)

	err := d.Client.ContainerRemove(context.Background(), d.Server.Id(), types.ContainerRemoveOptions{
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

	return err
}

// Determine the container exit state and return the exit code and wether or not
// the container was killed by the OOM killer.
func (d *DockerEnvironment) ExitState() (uint32, bool, error) {
	c, err := d.Client.ContainerInspect(context.Background(), d.Server.Id())
	if err != nil {
		// I'm not entirely sure how this can happen to be honest. I tried deleting a
		// container _while_ a server was running and wings gracefully saw the crash and
		// created a new container for it.
		//
		// However, someone reported an error in Discord about this scenario happening,
		// so I guess this should prevent it? They didn't tell me how they caused it though
		// so that's a mystery that will have to go unsolved.
		//
		// @see https://github.com/pterodactyl/panel/issues/2003
		if client.IsErrNotFound(err) {
			return 1, false, nil
		}

		return 0, false, errors.WithStack(err)
	}

	return uint32(c.State.ExitCode), c.State.OOMKilled, nil
}

// Attaches to the docker container itself and ensures that we can pipe data in and out
// of the process stream. This should not be used for reading console data as you *will*
// miss important output at the beginning because of the time delay with attaching to the
// output.
func (d *DockerEnvironment) Attach() error {
	if d.IsAttached() {
		return nil
	}

	if err := d.FollowConsoleOutput(); err != nil {
		return errors.WithStack(err)
	}

	var err error
	d.Lock()
	d.stream, err = d.Client.ContainerAttach(context.Background(), d.Server.Id(), types.ContainerAttachOptions{
		Stdin:  true,
		Stdout: true,
		Stderr: true,
		Stream: true,
	})
	d.Unlock()

	if err != nil {
		return errors.WithStack(err)
	}

	console := Console{
		Server: d.Server,
	}

	d.SetAttached(true)
	go func() {
		if err := d.EnableResourcePolling(); err != nil {
			d.Server.Log().WithField("error", errors.WithStack(err)).Warn("failed to enable resource polling on server")
		}
	}()

	go func() {
		defer d.stream.Close()
		defer func() {
			d.Server.SetState(ProcessOfflineState)
			d.SetAttached(false)
		}()

		io.Copy(console, d.stream.Reader)
	}()

	return nil
}

// Attaches to the log for the container. This avoids us missing cruicial output that
// happens in the split seconds before the code moves from 'Starting' to 'Attaching'
// on the process.
func (d *DockerEnvironment) FollowConsoleOutput() error {
	if exists, err := d.Exists(); !exists {
		if err != nil {
			return errors.WithStack(err)
		}

		return errors.New(fmt.Sprintf("no such container: %s", d.Server.Id()))
	}

	opts := types.ContainerLogsOptions{
		ShowStderr: true,
		ShowStdout: true,
		Follow:     true,
		Since:      time.Now().Format(time.RFC3339),
	}

	reader, err := d.Client.ContainerLogs(context.Background(), d.Server.Id(), opts)

	go func(r io.ReadCloser) {
		defer r.Close()

		s := bufio.NewScanner(r)
		for s.Scan() {
			d.Server.Events().Publish(ConsoleOutputEvent, s.Text())
		}

		if err := s.Err(); err != nil {
			d.Server.Log().WithField("error", err).Warn("error processing scanner line in console output")
		}
	}(reader)

	return errors.WithStack(err)
}

// Enables resource polling on the docker instance. Except we aren't actually polling Docker for this
// information, instead just sit there with an async process that lets Docker stream all of this data
// to us automatically.
func (d *DockerEnvironment) EnableResourcePolling() error {
	if d.Server.GetState() == ProcessOfflineState {
		return errors.New("cannot enable resource polling on a server that is not running")
	}

	stats, err := d.Client.ContainerStats(context.Background(), d.Server.Id(), true)
	if err != nil {
		return errors.WithStack(err)
	}
	d.stats = stats.Body

	dec := json.NewDecoder(d.stats)
	go func(s *Server) {
		for {
			var v *types.StatsJSON

			if err := dec.Decode(&v); err != nil {
				if err != io.EOF {
					d.Server.Log().WithField("error", err).Warn("encountered error processing server stats, stopping collection")
				}

				d.DisableResourcePolling()
				return
			}

			// Disable collection if the server is in an offline state and this process is
			// still running.
			if s.GetState() == ProcessOfflineState {
				d.DisableResourcePolling()
				return
			}

			s.Proc().UpdateFromDocker(v)
			for _, nw := range v.Networks {
				s.Proc().UpdateNetworkBytes(&nw)
			}

			// Why you ask? This already has the logic for caching disk space in use and then
			// also handles pushing that value to the resources object automatically.
			s.Filesystem.HasSpaceAvailable()

			b, _ := json.Marshal(s.Proc())
			s.Events().Publish(StatsEvent, string(b))
		}
	}(d.Server)

	return nil
}

// Closes the stats stream for a server process.
func (d *DockerEnvironment) DisableResourcePolling() error {
	if d.stats == nil {
		return nil
	}

	err := d.stats.Close()
	d.Server.Proc().Empty()

	return errors.WithStack(err)
}

// Returns the image to be used for the instance.
func (d *DockerEnvironment) Image() string {
	return d.Server.Config().Container.Image
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
// @todo handle authorization & local images
func (d *DockerEnvironment) ensureImageExists() error {
	// Give it up to 15 minutes to pull the image. I think this should cover 99.8% of cases where an
	// image pull might fail. I can't imagine it will ever take more than 15 minutes to fully pull
	// an image. Let me know when I am inevitably wrong here...
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute*15)
	defer cancel()

	image := d.Image()

	// Get a registry auth configuration from the config.
	var registryAuth *config.RegistryConfiguration
	for registry, c := range config.Get().Docker.Registries {
		if !strings.HasPrefix(image, registry) {
			continue
		}

		log.WithField("registry", registry).Debug("using authentication for repository")
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

	out, err := d.Client.ImagePull(ctx, image, imagePullOptions)
	if err != nil {
		images, ierr := d.Client.ImageList(ctx, types.ImageListOptions{})
		if ierr != nil {
			// Well damn, something has gone really wrong here, just go ahead and abort there
			// isn't much anything we can do to try and self-recover from this.
			return ierr
		}

		for _, img := range images {
			for _, t := range img.RepoTags {
				if t != d.Image() {
					continue
				}

				d.Server.Log().WithFields(log.Fields{
					"image": d.Image(),
					"error": errors.New(err.Error()),
				}).Warn("unable to pull requested image from remote source, however the image exists locally")

				// Okay, we found a matching container image, in that case just go ahead and return
				// from this function, since there is nothing else we need to do here.
				return nil
			}
		}

		return err
	}
	defer out.Close()

	log.WithField("image", d.Image()).Debug("pulling docker image... this could take a bit of time")

	// I'm not sure what the best approach here is, but this will block execution until the image
	// is done being pulled, which is what we need.
	scanner := bufio.NewScanner(out)
	for scanner.Scan() {
		continue
	}

	if err := scanner.Err(); err != nil {
		return err
	}

	return nil
}

// Creates a new container for the server using all of the data that is currently
// available for it. If the container already exists it will be returned.
func (d *DockerEnvironment) Create() error {
	// Ensure the data directory exists before getting too far through this process.
	if err := d.Server.Filesystem.EnsureDataDirectory(); err != nil {
		return errors.WithStack(err)
	}

	// If the container already exists don't hit the user with an error, just return
	// the current information about it which is what we would do when creating the
	// container anyways.
	if _, err := d.Client.ContainerInspect(context.Background(), d.Server.Id()); err == nil {
		return nil
	} else if !client.IsErrNotFound(err) {
		return errors.WithStack(err)
	}

	// Try to pull the requested image before creating the container.
	if err := d.ensureImageExists(); err != nil {
		return errors.WithStack(err)
	}

	conf := &container.Config{
		Hostname:     d.Server.Id(),
		Domainname:   config.Get().Docker.Domainname,
		User:         strconv.Itoa(config.Get().System.User.Uid),
		AttachStdin:  true,
		AttachStdout: true,
		AttachStderr: true,
		OpenStdin:    true,
		Tty:          true,
		ExposedPorts: d.exposedPorts(),
		Image:        d.Image(),
		Env:          d.Server.GetEnvironmentVariables(),
		Labels: map[string]string{
			"Service":       "Pterodactyl",
			"ContainerType": "server_process",
		},
	}

	mounts := []mount.Mount{
		{
			Target:   "/home/container",
			Source:   d.Server.Filesystem.Path(),
			Type:     mount.TypeBind,
			ReadOnly: false,
		},
	}

	var mounted bool
	for _, m := range d.Server.Config().Mounts {
		mounted = false
		source := filepath.Clean(m.Source)
		target := filepath.Clean(m.Target)

		for _, allowed := range config.Get().AllowedMounts {
			if !strings.HasPrefix(source, allowed) {
				continue
			}

			mounts = append(mounts, mount.Mount{
				Type: mount.TypeBind,

				Source:   source,
				Target:   target,
				ReadOnly: m.ReadOnly,
			})

			mounted = true
			break
		}

		logger := log.WithFields(log.Fields{
			"server":      d.Server.Id(),
			"source_path": source,
			"target_path": target,
			"read_only":   m.ReadOnly,
		})

		if mounted {
			logger.Debug("attaching mount to server's container")
		} else {
			logger.Warn("skipping mount because it isn't allowed")
		}
	}

	hostConf := &container.HostConfig{
		PortBindings: d.portBindings(),

		// Configure the mounts for this container. First mount the server data directory
		// into the container as a r/w bind.
		Mounts: mounts,

		// Configure the /tmp folder mapping in containers. This is necessary for some
		// games that need to make use of it for downloads and other installation processes.
		Tmpfs: map[string]string{
			"/tmp": "rw,exec,nosuid,size=50M",
		},

		// Define resource limits for the container based on the data passed through
		// from the Panel.
		Resources: d.getResourcesForServer(),

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

	if _, err := d.Client.ContainerCreate(context.Background(), conf, hostConf, nil, d.Server.Id()); err != nil {
		return errors.WithStack(err)
	}

	return nil
}

// Sends the specified command to the stdin of the running container instance. There is no
// confirmation that this data is sent successfully, only that it gets pushed into the stdin.
func (d *DockerEnvironment) SendCommand(c string) error {
	if !d.IsAttached() {
		return errors.New("attempting to send command to non-attached instance")
	}

	_, err := d.stream.Conn.Write([]byte(c + "\n"))

	return errors.WithStack(err)
}

// Reads the log file for the server. This does not care if the server is running or not, it will
// simply try to read the last X bytes of the file and return them.
func (d *DockerEnvironment) Readlog(len int64) ([]string, error) {
	j, err := d.Client.ContainerInspect(context.Background(), d.Server.Id())
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
	Log string `json:"log"`
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

// Converts the server allocation mappings into a format that can be understood
// by Docker.
func (d *DockerEnvironment) portBindings() nat.PortMap {
	var out = nat.PortMap{}

	for ip, ports := range d.Server.Config().Allocations.Mappings {
		for _, port := range ports {
			// Skip over invalid ports.
			if port < 1 || port > 65535 {
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

// Formats the resources available to a server instance in such as way that Docker will
// generate a matching environment in the container.
//
// This will set the actual memory limit on the container using the multiplier which is the
// hard limit for the container (after which will result in a crash). We then set the
// reservation to be the expected memory limit based on simply multiplication.
//
// The swap value is either -1 to disable it, or set to the value of the hard memory limit
// plus the additional swap assigned to the server since Docker expects this value to be
// the same or higher than the memory limit.
func (d *DockerEnvironment) getResourcesForServer() container.Resources {
	return container.Resources{
		Memory:            d.Server.Build().BoundedMemoryLimit(),
		MemoryReservation: d.Server.Build().MemoryLimit * 1_000_000,
		MemorySwap:        d.Server.Build().ConvertedSwap(),
		CPUQuota:          d.Server.Build().ConvertedCpuLimit(),
		CPUPeriod:         100_000,
		CPUShares:         1024,
		BlkioWeight:       d.Server.Build().IoWeight,
		OomKillDisable:    &d.Server.Config().Container.OomDisabled,
		CpusetCpus:        d.Server.Build().Threads,
	}
}
