package environment

import (
	"context"
	"github.com/apex/log"
	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
	"github.com/pkg/errors"
	"github.com/pterodactyl/wings/api"
	"github.com/pterodactyl/wings/system"
	"os"
	"strings"
	"time"
)

// Run before the container starts and get the process configuration from the Panel.
// This is important since we use this to check configuration files as well as ensure
// we always have the latest version of an egg available for server processes.
//
// This process will also confirm that the server environment exists and is in a bootable
// state. This ensures that unexpected container deletion while Wings is running does
// not result in the server becoming unbootable.
func (d *DockerEnvironment) OnBeforeStart() error {
	// d.Server.Log().Info("syncing server configuration with panel")
	// if err := d.Server.Sync(); err != nil {
	// 	return err
	// }

	// if !d.Server.Filesystem.HasSpaceAvailable() {
	// 	return errors.New("cannot start server, not enough disk space available")
	// }

	// Always destroy and re-create the server container to ensure that synced data from
	// the Panel is used.
	if err := d.client.ContainerRemove(context.Background(), d.Id, types.ContainerRemoveOptions{RemoveVolumes: true}); err != nil {
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
//
// TODO: check if the server is suspended before calling this.
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
			d.setState(system.ProcessStoppingState)
			d.setState(system.ProcessOfflineState)
		}
	}()

	if c, err := d.client.ContainerInspect(context.Background(), d.Id); err != nil {
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
			d.setState(system.ProcessRunningState)

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

	d.setState(system.ProcessStartingState)

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
	// d.Server.UpdateConfigurationFiles()
	//
	// // Reset the permissions on files for the server before actually trying
	// // to start it.
	// if err := d.Server.Filesystem.Chown("/"); err != nil {
	// 	return errors.WithStack(err)
	// }

	ctx, cancel := context.WithTimeout(context.Background(), time.Second*10)
	defer cancel()

	if err := d.client.ContainerStart(ctx, d.Id, types.ContainerStartOptions{}); err != nil {
		return errors.WithStack(err)
	}

	// No errors, good to continue through.
	sawError = false

	return d.Attach()
}

// Restarts the server process by waiting for the process to gracefully stop and then triggering a
// start command. This will return an error if there is already a restart process executing for the
// server. The lock is released when the process is stopped and a start has begun.
func (d *DockerEnvironment) Restart() error {
	err := d.WaitForStop(60, false)
	if err != nil {
		return err
	}

	// Start the process.
	return d.Start()
}

// Stops the container that the server is running in. This will allow up to 10
// seconds to pass before a failure occurs.
func (d *DockerEnvironment) Stop(c api.ProcessStopConfiguration) error {
	if c.Type == api.ProcessStopSignal {
		return d.Terminate(os.Kill)
	}

	d.setState(system.ProcessStoppingState)

	// Only attempt to send the stop command to the instance if we are actually attached to
	// the instance. If we are not for some reason, just send the container stop event.
	if d.IsAttached() && c.Type == api.ProcessStopCommand {
		return d.SendCommand(c.Value)
	}

	t := time.Second * 10

	err := d.client.ContainerStop(context.Background(), d.Id, &t)
	if err != nil {
		// If the container does not exist just mark the process as stopped and return without
		// an error.
		if client.IsErrNotFound(err) {
			d.SetStream(nil)
			d.setState(system.ProcessOfflineState)

			return nil
		}

		return err
	}

	return nil
}

// Attempts to gracefully stop a server using the defined stop command. If the server
// does not stop after seconds have passed, an error will be returned, or the instance
// will be terminated forcefully depending on the value of the second argument.
func (d *DockerEnvironment) WaitForStop(c api.ProcessStopConfiguration, seconds int, terminate bool) error {
	if err := d.Stop(c); err != nil {
		return errors.WithStack(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(seconds)*time.Second)
	defer cancel()

	// Block the return of this function until the container as been marked as no
	// longer running. If this wait does not end by the time seconds have passed,
	// attempt to terminate the container, or return an error.
	ok, errChan := d.client.ContainerWait(ctx, d.Id, container.WaitConditionNotRunning)
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
	c, err := d.client.ContainerInspect(context.Background(), d.Id)
	if err != nil {
		return errors.WithStack(err)
	}

	if !c.State.Running {
		return nil
	}

	// We set it to stopping than offline to prevent crash detection from being triggered.
	d.setState(system.ProcessStoppingState)

	sig := strings.TrimSuffix(strings.TrimPrefix(signal.String(), "signal "), "ed")

	if err := d.client.ContainerKill(context.Background(), d.Id, sig); err != nil {
		return err
	}

	d.setState(system.ProcessOfflineState)

	return nil
}
