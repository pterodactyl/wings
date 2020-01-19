package server

import (
	"bufio"
	"bytes"
	"context"
	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/client"
	"github.com/pkg/errors"
	"github.com/pterodactyl/wings/api"
	"go.uber.org/zap"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"sync"
)

func (s *Server) Install() error {
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

	go func() {
		zap.S().Infow("beginning installation process for server", zap.String("server", s.Uuid))

		if err := p.Run(); err != nil {
			zap.S().Errorw("failed to complete installation process for server", zap.String("server", s.Uuid), zap.Error(err))
		}

		zap.S().Infow("completed installation process for server", zap.String("server", s.Uuid))
	}()

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
		return err
	}

	// If this step fails, log a warning but don't exit out of the process. This is completely
	// internal to the daemon's functionality, and does not affect the status of the server itself.
	if err := ip.AfterExecute(cid); err != nil {
		zap.S().Warnw("failed to complete after-execute step of installation process", zap.String("server", ip.Server.Uuid), zap.Error(err))
	}

	return nil
}

// Writes the installation script to a temporary file on the host machine so that it
// can be properly mounted into the installation container and then executed.
func (ip *InstallationProcess) writeScriptToDisk() (string, error) {
	d, err := ioutil.TempDir("", "pterodactyl")
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
		zap.S().Debugw(scanner.Text())
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

// Cleans up after the execution of the installation process. This grabs the logs from the
// process to store in the server configuration directory, and then destroys the associated
// installation container.
func (ip *InstallationProcess) AfterExecute(containerId string) error {
	ctx := context.Background()

	zap.S().Debugw("pulling installation logs for server", zap.String("server", ip.Server.Uuid), zap.String("container_id", containerId))
	reader, err := ip.client.ContainerLogs(ctx, containerId, types.ContainerLogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Follow:     false,
	})

	if err != nil && !client.IsErrNotFound(err) {
		return errors.WithStack(err)
	}

	f, err := os.OpenFile(filepath.Join("data/install_logs/", ip.Server.Uuid+".log"), os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return errors.WithStack(err)
	}
	defer f.Close()

	// We write the contents of the container output to a more "permanent" file so that they
	// can be referenced after this container is deleted.
	if _, err := io.Copy(f, reader); err != nil {
		return errors.WithStack(err)
	}

	zap.S().Debugw("removing server installation container", zap.String("server", ip.Server.Uuid), zap.String("container_id", containerId))
	rErr := ip.client.ContainerRemove(ctx, containerId, types.ContainerRemoveOptions{
		RemoveVolumes: true,
		RemoveLinks:   false,
		Force:         true,
	})

	if rErr != nil && !client.IsErrNotFound(rErr) {
		return errors.WithStack(rErr)
	}

	return nil
}

// Executes the installation process inside a specially created docker container.
func (ip *InstallationProcess) Execute(installPath string) (string, error) {
	ctx := context.Background()

	zap.S().Debugw(
		"creating server installer container",
		zap.String("server", ip.Server.Uuid),
		zap.String("script_path", installPath+"/install.sh"),
	)

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
		DNS: []string{"1.1.1.1", "8.8.8.8"},
		LogConfig: container.LogConfig{
			Type: "local",
			Config: map[string]string{
				"max-size": "5m",
				"max-file": "1",
				"compress": "false",
			},
		},
		Privileged:  true,
		NetworkMode: "pterodactyl_nw",
	}

	zap.S().Infow("creating installer container for server process", zap.String("server", ip.Server.Uuid))
	r, err := ip.client.ContainerCreate(ctx, conf, hostConf, nil, ip.Server.Uuid+"_installer")
	if err != nil {
		return "", errors.WithStack(err)
	}

	zap.S().Infow(
		"running installation script for server in container",
		zap.String("server", ip.Server.Uuid),
		zap.String("container_id", r.ID),
	)
	if err := ip.client.ContainerStart(ctx, r.ID, types.ContainerStartOptions{}); err != nil {
		return "", err
	}

	go func(id string) {
		ip.Server.Events().Publish(DaemonMessageEvent, "Starting installation process, this could take a few minutes...")
		if err := ip.StreamOutput(id); err != nil {
			zap.S().Errorw(
				"error handling streaming output for server install process",
				zap.String("container_id", id),
				zap.Error(err),
			)
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
		zap.S().Warnw(
			"error processing scanner line in installation output for server",
			zap.String("server", ip.Server.Uuid),
			zap.String("container_id", id),
			zap.Error(errors.WithStack(err)),
		)
	}

	return nil
}
