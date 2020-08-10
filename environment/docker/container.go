package docker

import (
	"bufio"
	"context"
	"fmt"
	"github.com/apex/log"
	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
	"github.com/pkg/errors"
	"github.com/pterodactyl/wings/config"
	"github.com/pterodactyl/wings/events"
	"github.com/pterodactyl/wings/system"
	"io"
	"strings"
	"time"
)

// Attaches to the docker container itself and ensures that we can pipe data in and out
// of the process stream. This should not be used for reading console data as you *will*
// miss important output at the beginning because of the time delay with attaching to the
// output.
func (d *Environment) Attach() error {
	if d.IsAttached() {
		return nil
	}

	if err := d.followOutput(); err != nil {
		return errors.WithStack(err)
	}

	opts := types.ContainerAttachOptions{
		Stdin:  true,
		Stdout: true,
		Stderr: true,
		Stream: true,
	}

	// Set the stream again with the container.
	if st, err := d.client.ContainerAttach(context.Background(), d.Id, opts); err != nil {
		return errors.WithStack(err)
	} else {
		d.SetStream(&st)
	}

	console := new(Console)

	// TODO: resource polling should be handled by the server itself and just call a function
	//  on the environment that can return the data. Same for disabling polling.
	go func() {
		defer d.stream.Close()
		defer func() {
			d.setState(system.ProcessOfflineState)
			d.SetStream(nil)
		}()

		_, _ = io.Copy(console, d.stream.Reader)
	}()

	return nil
}

// Performs an in-place update of the Docker container's resource limits without actually
// making any changes to the operational state of the container. This allows memory, cpu,
// and IO limitations to be adjusted on the fly for individual instances.
func (d *Environment) InSituUpdate() error {
	if _, err := d.client.ContainerInspect(context.Background(), d.Id); err != nil {
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
		// TODO: get the resources from the server. I suppose they should be passed through
		//  in a struct.
		// Resources: d.getResourcesForServer(),
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second*10)
	defer cancel()
	if _, err := d.client.ContainerUpdate(ctx, d.Id, u); err != nil {
		return errors.WithStack(err)
	}

	return nil
}

// Remove the Docker container from the machine. If the container is currently running
// it will be forcibly stopped by Docker.
func (d *Environment) Destroy() error {
	// We set it to stopping than offline to prevent crash detection from being triggered.
	d.setState(system.ProcessStoppingState)

	err := d.client.ContainerRemove(context.Background(), d.Id, types.ContainerRemoveOptions{
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

	d.setState(system.ProcessOfflineState)

	return err
}

// Attaches to the log for the container. This avoids us missing cruicial output that
// happens in the split seconds before the code moves from 'Starting' to 'Attaching'
// on the process.
func (d *Environment) followOutput() error {
	if exists, err := d.Exists(); !exists {
		if err != nil {
			return errors.WithStack(err)
		}

		return errors.New(fmt.Sprintf("no such container: %s", d.Id))
	}

	opts := types.ContainerLogsOptions{
		ShowStderr: true,
		ShowStdout: true,
		Follow:     true,
		Since:      time.Now().Format(time.RFC3339),
	}

	reader, err := d.client.ContainerLogs(context.Background(), d.Id, opts)

	go func(r io.ReadCloser) {
		defer r.Close()

		s := bufio.NewScanner(r)
		for s.Scan() {
			d.Events().Publish(events.EnvironmentConsoleOutput, s.Text())
		}

		if err := s.Err(); err != nil {
			log.WithField("error", err).WithField("container_id", d.Id).Warn("error processing scanner line in console output")
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
// TODO: handle authorization & local images
func (d *Environment) ensureImageExists(image string) error {
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

	out, err := d.client.ImagePull(ctx, image, imagePullOptions)
	if err != nil {
		images, ierr := d.client.ImageList(ctx, types.ImageListOptions{})
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
					"image": image,
					"container_id": d.Id,
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

	log.WithField("image", image).Debug("pulling docker image... this could take a bit of time")

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