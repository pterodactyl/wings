package docker

import (
	"context"
	"os"
	"strings"
	"syscall"
	"time"

	"emperror.dev/errors"
	"github.com/apex/log"
	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"

	"github.com/pterodactyl/wings/environment"
	"github.com/pterodactyl/wings/remote"
)

// OnBeforeStart run before the container starts and get the process
// configuration from the Panel. This is important since we use this to check
// configuration files as well as ensure we always have the latest version of
// an egg available for server processes.
//
// This process will also confirm that the server environment exists and is in
// a bootable state. This ensures that unexpected container deletion while Wings
// is running does not result in the server becoming un-bootable.
func (e *Environment) OnBeforeStart(ctx context.Context) error {
	// Always destroy and re-create the server container to ensure that synced data from the Panel is used.
	if err := e.client.ContainerRemove(ctx, e.Id, types.ContainerRemoveOptions{RemoveVolumes: true}); err != nil {
		if !client.IsErrNotFound(err) {
			return errors.WrapIf(err, "environment/docker: failed to remove container during pre-boot")
		}
	}

	// The Create() function will check if the container exists in the first place, and if
	// so just silently return without an error. Otherwise, it will try to create the necessary
	// container and data storage directory.
	//
	// This won't actually run an installation process however, it is just here to ensure the
	// environment gets created properly if it is missing and the server is started. We're making
	// an assumption that all of the files will still exist at this point.
	if err := e.Create(); err != nil {
		return err
	}

	return nil
}

// Start will start the server environment and begins piping output to the event
// listeners for the console. If a container does not exist, or needs to be
// rebuilt that will happen in the call to OnBeforeStart().
func (e *Environment) Start(ctx context.Context) error {
	sawError := false

	// If sawError is set to true there was an error somewhere in the pipeline that
	// got passed up, but we also want to ensure we set the server to be offline at
	// that point.
	defer func() {
		if sawError {
			// If we don't set it to stopping first, you'll trigger crash detection which
			// we don't want to do at this point since it'll just immediately try to do the
			// exact same action that lead to it crashing in the first place...
			e.SetState(environment.ProcessStoppingState)
			e.SetState(environment.ProcessOfflineState)
		}
	}()

	if c, err := e.client.ContainerInspect(ctx, e.Id); err != nil {
		// Do nothing if the container is not found, we just don't want to continue
		// to the next block of code here. This check was inlined here to guard against
		// a nil-pointer when checking c.State below.
		//
		// @see https://github.com/pterodactyl/panel/issues/2000
		if !client.IsErrNotFound(err) {
			return errors.WrapIf(err, "environment/docker: failed to inspect container")
		}
	} else {
		// If the server is running update our internal state and continue on with the attach.
		if c.State.Running {
			e.SetState(environment.ProcessRunningState)

			return e.Attach(ctx)
		}

		// Truncate the log file, so we don't end up outputting a bunch of useless log information
		// to the websocket and whatnot. Check first that the path and file exist before trying
		// to truncate them.
		if _, err := os.Stat(c.LogPath); err == nil {
			if err := os.Truncate(c.LogPath, 0); err != nil {
				return errors.Wrap(err, "environment/docker: failed to truncate instance logs")
			}
		}
	}

	e.SetState(environment.ProcessStartingState)

	// Set this to true for now, we will set it to false once we reach the
	// end of this chain.
	sawError = true

	// Run the before start function and wait for it to finish. This will validate that the container
	// exists on the system, and rebuild the container if that is required for server booting to
	// occur.
	if err := e.OnBeforeStart(ctx); err != nil {
		return errors.WithStackIf(err)
	}

	// If we cannot start & attach to the container in 30 seconds something has gone
	// quite sideways and we should stop trying to avoid a hanging situation.
	actx, cancel := context.WithTimeout(ctx, time.Second*30)
	defer cancel()

	if err := e.client.ContainerStart(actx, e.Id, types.ContainerStartOptions{}); err != nil {
		return errors.WrapIf(err, "environment/docker: failed to start container")
	}

	// No errors, good to continue through.
	sawError = false

	return e.Attach(actx)
}

// Stop stops the container that the server is running in. This will allow up to
// 30 seconds to pass before the container is forcefully terminated if we are
// trying to stop it without using a command sent into the instance.
//
// You most likely want to be using WaitForStop() rather than this function,
// since this will return as soon as the command is sent, rather than waiting
// for the process to be completed stopped.
//
// TODO: pass context through from the server instance.
func (e *Environment) Stop() error {
	e.mu.RLock()
	s := e.meta.Stop
	e.mu.RUnlock()

	// A native "stop" as the Type field value will just skip over all of this
	// logic and end up only executing the container stop command (which may or
	// may not work as expected).
	if s.Type == "" || s.Type == remote.ProcessStopSignal {
		if s.Type == "" {
			log.WithField("container_id", e.Id).Warn("no stop configuration detected for environment, using termination procedure")
		}

		signal := os.Kill
		// Handle a few common cases, otherwise just fall through and just pass along
		// the os.Kill signal to the process.
		switch strings.ToUpper(s.Value) {
		case "SIGABRT":
			signal = syscall.SIGABRT
		case "SIGINT":
			signal = syscall.SIGINT
		case "SIGTERM":
			signal = syscall.SIGTERM
		}
		return e.Terminate(signal)
	}

	// If the process is already offline don't switch it back to stopping. Just leave it how
	// it is and continue through to the stop handling for the process.
	if e.st.Load() != environment.ProcessOfflineState {
		e.SetState(environment.ProcessStoppingState)
	}

	// Only attempt to send the stop command to the instance if we are actually attached to
	// the instance. If we are not for some reason, just send the container stop event.
	if e.IsAttached() && s.Type == remote.ProcessStopCommand {
		return e.SendCommand(s.Value)
	}

	t := time.Second * 30
	if err := e.client.ContainerStop(context.Background(), e.Id, &t); err != nil {
		// If the container does not exist just mark the process as stopped and return without
		// an error.
		if client.IsErrNotFound(err) {
			e.SetStream(nil)
			e.SetState(environment.ProcessOfflineState)
			return nil
		}
		return errors.Wrap(err, "environment/docker: cannot stop container")
	}

	return nil
}

// WaitForStop attempts to gracefully stop a server using the defined stop
// command. If the server does not stop after seconds have passed, an error will
// be returned, or the instance will be terminated forcefully depending on the
// value of the second argument.
func (e *Environment) WaitForStop(seconds uint, terminate bool) error {
	if err := e.Stop(); err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(seconds)*time.Second)
	defer cancel()

	// Block the return of this function until the container as been marked as no
	// longer running. If this wait does not end by the time seconds have passed,
	// attempt to terminate the container, or return an error.
	ok, errChan := e.client.ContainerWait(ctx, e.Id, container.WaitConditionNotRunning)
	select {
	case <-ctx.Done():
		if ctxErr := ctx.Err(); ctxErr != nil {
			if terminate {
				log.WithField("container_id", e.Id).Info("server did not stop in time, executing process termination")

				return e.Terminate(os.Kill)
			}

			return ctxErr
		}
	case err := <-errChan:
		// If the error stems from the container not existing there is no point in wasting
		// CPU time to then try and terminate it.
		if err != nil && !client.IsErrNotFound(err) {
			if terminate {
				l := log.WithField("container_id", e.Id)
				if errors.Is(err, context.DeadlineExceeded) {
					l.Warn("deadline exceeded for container stop; terminating process")
				} else {
					l.WithField("error", err).Warn("error while waiting for container stop; terminating process")
				}

				return e.Terminate(os.Kill)
			}
			return errors.WrapIf(err, "environment/docker: error waiting on container to enter \"not-running\" state")
		}
	case <-ok:
	}

	return nil
}

// Terminate forcefully terminates the container using the signal provided.
func (e *Environment) Terminate(signal os.Signal) error {
	c, err := e.client.ContainerInspect(context.Background(), e.Id)
	if err != nil {
		// Treat missing containers as an okay error state, means it is obviously
		// already terminated at this point.
		if client.IsErrNotFound(err) {
			return nil
		}
		return errors.WithStack(err)
	}

	if !c.State.Running {
		// If the container is not running, but we're not already in a stopped state go ahead
		// and update things to indicate we should be completely stopped now. Set to stopping
		// first so crash detection is not triggered.
		if e.st.Load() != environment.ProcessOfflineState {
			e.SetState(environment.ProcessStoppingState)
			e.SetState(environment.ProcessOfflineState)
		}

		return nil
	}

	// We set it to stopping than offline to prevent crash detection from being triggered.
	e.SetState(environment.ProcessStoppingState)
	sig := strings.TrimSuffix(strings.TrimPrefix(signal.String(), "signal "), "ed")
	if err := e.client.ContainerKill(context.Background(), e.Id, sig); err != nil && !client.IsErrNotFound(err) {
		return errors.WithStack(err)
	}
	e.SetState(environment.ProcessOfflineState)

	return nil
}
