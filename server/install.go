package server

import (
	"bufio"
	"bytes"
	"context"
	"github.com/apex/log"
	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/client"
	"github.com/pkg/errors"
	"github.com/pterodactyl/wings/api"
	"github.com/pterodactyl/wings/config"
	"io"
	"io/ioutil"
	"os"
	"path"
	"path/filepath"
	"sync"
)

// Executes the installation stack for a server process. Bubbles any errors up to the calling
// function which should handle contacting the panel to notify it of the server state.
func (s *Server) Install() error {
	err := s.internalInstall()

	s.Log().Debug("notifying panel of server install state")
	if serr := s.SyncInstallState(err == nil); serr != nil {
		l := s.Log().WithField("was_successful", err == nil)

		// If the request was successful but there was an error with this request, attach the
		// error to this log entry. Otherwise ignore it in this log since whatever is calling
		// this function should handle the error and will end up logging the same one.
		if err == nil {
			l.WithField("error", serr)
		}

		l.Warn("failed to notify panel of server install state")
	}

	return err
}

// Reinstalls a server's software by utilizing the install script for the server egg. This
// does not touch any existing files for the server, other than what the script modifies.
func (s *Server) Reinstall() error {
	if s.GetState() != ProcessOfflineState {
		s.Log().Debug("waiting for server instance to enter a stopped state")
		if err := s.Environment.WaitForStop(10, true); err != nil {
			return err
		}
	}

	return s.Install()
}

// Internal installation function used to simplify reporting back to the Panel.
func (s *Server) internalInstall() error {
	script, rerr, err := api.NewRequester().GetInstallationScript(s.Uuid)
	if err != nil || rerr != nil {
		if err != nil {
			return err
		}

		return errors.New(rerr.String())
	}

	p, err := NewInstallationProcess(s, &script)
	if err != nil {
		return errors.WithStack(err)
	}

	s.Log().Info("beginning installation process for server")
	if err := p.Run(); err != nil {
		return err
	}

	s.Log().Info("completed installation process for server")
	return nil
}

type InstallationProcess struct {
	Server *Server
	Script *api.InstallationScript

	client *client.Client
	mutex  *sync.Mutex
}

// Generates a new installation process struct that will be used to create containers,
// and otherwise perform installation commands for a server.
func NewInstallationProcess(s *Server, script *api.InstallationScript) (*InstallationProcess, error) {
	proc := &InstallationProcess{
		Script: script,
		Server: s,
		mutex:  &sync.Mutex{},
	}

	if c, err := client.NewClientWithOpts(client.FromEnv); err != nil {
		return nil, errors.WithStack(err)
	} else {
		proc.client = c
	}

	return proc, nil
}

// Removes the installer container for the server.
func (ip *InstallationProcess) RemoveContainer() {
	err := ip.client.ContainerRemove(context.Background(), ip.Server.Uuid+"_installer", types.ContainerRemoveOptions{
		RemoveVolumes: true,
		Force:         true,
	})

	if err != nil && !client.IsErrNotFound(err) {
		ip.Server.Log().WithField("error", errors.WithStack(err)).Warn("failed to delete server install container")
	}
}

// Runs the installation process, this is done as a backgrounded thread. This will configure
// the required environment, and then spin up the installation container.
//
// Once the container finishes installing the results will be stored in an installation
// log in the server's configuration directory.
func (ip *InstallationProcess) Run() error {
	installPath, err := ip.BeforeExecute()
	if err != nil {
		return err
	}

	cid, err := ip.Execute(installPath)
	if err != nil {
		ip.RemoveContainer()

		return err
	}

	// If this step fails, log a warning but don't exit out of the process. This is completely
	// internal to the daemon's functionality, and does not affect the status of the server itself.
	if err := ip.AfterExecute(cid); err != nil {
		ip.Server.Log().WithField("error", err).Warn("failed to complete after-execute step of installation process")
	}

	return nil
}

// Writes the installation script to a temporary file on the host machine so that it
// can be properly mounted into the installation container and then executed.
func (ip *InstallationProcess) writeScriptToDisk() (string, error) {
	// Make sure the temp directory root exists before trying to make a directory within it. The
	// ioutil.TempDir call expects this base to exist, it won't create it for you.
	if err := os.MkdirAll(path.Join(os.TempDir(), "pterodactyl/"), 0700); err != nil {
		return "", errors.WithStack(err)
	}

	d, err := ioutil.TempDir("", "pterodactyl/")
	if err != nil {
		return "", errors.WithStack(err)
	}

	f, err := os.OpenFile(filepath.Join(d, "install.sh"), os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		return "", errors.WithStack(err)
	}
	defer f.Close()

	w := bufio.NewWriter(f)

	scanner := bufio.NewScanner(bytes.NewReader([]byte(ip.Script.Script)))
	for scanner.Scan() {
		w.WriteString(scanner.Text() + "\n")
	}

	if err := scanner.Err(); err != nil {
		return "", errors.WithStack(err)
	}

	w.Flush()

	return d, nil
}

// Pulls the docker image to be used for the installation container.
func (ip *InstallationProcess) pullInstallationImage() error {
	r, err := ip.client.ImagePull(context.Background(), ip.Script.ContainerImage, types.ImagePullOptions{})
	if err != nil {
		return errors.WithStack(err)
	}

	// Block continuation until the image has been pulled successfully.
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		log.Debug(scanner.Text())
	}

	if err := scanner.Err(); err != nil {
		return errors.WithStack(err)
	}

	return nil
}

// Runs before the container is executed. This pulls down the required docker container image
// as well as writes the installation script to the disk. This process is executed in an async
// manner, if either one fails the error is returned.
func (ip *InstallationProcess) BeforeExecute() (string, error) {
	wg := sync.WaitGroup{}
	wg.Add(3)

	var e []error
	var fileName string

	go func() {
		defer wg.Done()
		name, err := ip.writeScriptToDisk()
		if err != nil {
			e = append(e, err)
			return
		}

		fileName = name
	}()

	go func() {
		defer wg.Done()
		if err := ip.pullInstallationImage(); err != nil {
			e = append(e, err)
		}
	}()

	go func() {
		defer wg.Done()

		opts := types.ContainerRemoveOptions{
			RemoveVolumes: true,
			Force:         true,
		}

		if err := ip.client.ContainerRemove(context.Background(), ip.Server.Uuid+"_installer", opts); err != nil {
			if !client.IsErrNotFound(err) {
				e = append(e, err)
			}
		}
	}()

	wg.Wait()

	// Maybe a better way to handle this, but if there is at least one error
	// just bail out of the process now.
	if len(e) > 0 {
		return "", errors.WithStack(e[0])
	}

	return fileName, nil
}

// Returns the log path for the installation process.
func (ip *InstallationProcess) GetLogPath() string {
	return filepath.Join(config.Get().System.GetInstallLogPath(), ip.Server.Uuid+".log")
}

// Cleans up after the execution of the installation process. This grabs the logs from the
// process to store in the server configuration directory, and then destroys the associated
// installation container.
func (ip *InstallationProcess) AfterExecute(containerId string) error {
	ctx := context.Background()
	defer ip.RemoveContainer()

	ip.Server.Log().WithField("container_id", containerId).Debug("pulling installation logs for server")
	reader, err := ip.client.ContainerLogs(ctx, containerId, types.ContainerLogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Follow:     false,
	})

	if err != nil && !client.IsErrNotFound(err) {
		return errors.WithStack(err)
	}

	f, err := os.OpenFile(ip.GetLogPath(), os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return errors.WithStack(err)
	}
	defer f.Close()

	// We write the contents of the container output to a more "permanent" file so that they
	// can be referenced after this container is deleted.
	if _, err := io.Copy(f, reader); err != nil {
		return errors.WithStack(err)
	}

	return nil
}

// Executes the installation process inside a specially created docker container.
func (ip *InstallationProcess) Execute(installPath string) (string, error) {
	ctx := context.Background()

	conf := &container.Config{
		Hostname:     "installer",
		AttachStdout: true,
		AttachStderr: true,
		AttachStdin:  true,
		OpenStdin:    true,
		Tty:          true,
		Cmd:          []string{ip.Script.Entrypoint, "./mnt/install/install.sh"},
		Image:        ip.Script.ContainerImage,
		Env:          ip.Server.GetEnvironmentVariables(),
		Labels: map[string]string{
			"Service":       "Pterodactyl",
			"ContainerType": "server_installer",
		},
	}

	hostConf := &container.HostConfig{
		Mounts: []mount.Mount{
			{
				Target:   "/mnt/server",
				Source:   ip.Server.Filesystem.Path(),
				Type:     mount.TypeBind,
				ReadOnly: false,
			},
			{
				Target:   "/mnt/install",
				Source:   installPath,
				Type:     mount.TypeBind,
				ReadOnly: false,
			},
		},
		Tmpfs: map[string]string{
			"/tmp": "rw,exec,nosuid,size=50M",
		},
		DNS: config.Get().Docker.Network.Dns,
		LogConfig: container.LogConfig{
			Type: "local",
			Config: map[string]string{
				"max-size": "5m",
				"max-file": "1",
				"compress": "false",
			},
		},
		Privileged:  true,
		NetworkMode: container.NetworkMode(config.Get().Docker.Network.Mode),
	}

	ip.Server.Log().WithField("install_script", installPath+"/install.sh").Info("creating install container for server process")
	r, err := ip.client.ContainerCreate(ctx, conf, hostConf, nil, ip.Server.Uuid+"_installer")
	if err != nil {
		return "", errors.WithStack(err)
	}

	ip.Server.Log().WithField("container_id", r.ID).Info("running installation script for server in container")
	if err := ip.client.ContainerStart(ctx, r.ID, types.ContainerStartOptions{}); err != nil {
		return "", err
	}

	go func(id string) {
		ip.Server.Events().Publish(DaemonMessageEvent, "Starting installation process, this could take a few minutes...")
		if err := ip.StreamOutput(id); err != nil {
			ip.Server.Log().WithField("error", err).Error("error while handling output stream for server install process")
		}
		ip.Server.Events().Publish(DaemonMessageEvent, "Installation process completed.")
	}(r.ID)

	sChann, eChann := ip.client.ContainerWait(ctx, r.ID, container.WaitConditionNotRunning)
	select {
	case err := <-eChann:
		if err != nil {
			return "", errors.WithStack(err)
		}
	case <-sChann:
	}

	return r.ID, nil
}

// Streams the output of the installation process to a log file in the server configuration
// directory, as well as to a websocket listener so that the process can be viewed in
// the panel by administrators.
func (ip *InstallationProcess) StreamOutput(id string) error {
	reader, err := ip.client.ContainerLogs(context.Background(), id, types.ContainerLogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Follow:     true,
	})

	if err != nil {
		return errors.WithStack(err)
	}

	defer reader.Close()

	s := bufio.NewScanner(reader)
	for s.Scan() {
		ip.Server.Events().Publish(InstallOutputEvent, s.Text())
	}

	if err := s.Err(); err != nil {
		ip.Server.Log().WithFields(log.Fields{
			"container_id": id,
			"error": errors.WithStack(err),
		}).Warn("error processing scanner line in installation output for server")
	}

	return nil
}

// Makes a HTTP request to the Panel instance notifying it that the server has
// completed the installation process, and what the state of the server is. A boolean
// value of "true" means everything was successful, "false" means something went
// wrong and the server must be deleted and re-created.
func (s *Server) SyncInstallState(successful bool) error {
	r := api.NewRequester()

	rerr, err := r.SendInstallationStatus(s.Uuid, successful)
	if rerr != nil || err != nil {
		if err != nil {
			return errors.WithStack(err)
		}

		return errors.New(rerr.String())
	}

	return nil
}
