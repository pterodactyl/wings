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

	return p.Execute()
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
		w.WriteString(scanner.Text()+"\n")
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

	// Copy to stdout until we hit an EOF or other fatal error which would
	// require exiting.
	if _, err := io.Copy(os.Stdout, r); err != nil && err != io.EOF {
		return errors.WithStack(err)
	}

	return nil
}

// Runs before the container is executed. This pulls down the required docker container image
// as well as writes the installation script to the disk. This process is executed in an async
// manner, if either one fails the error is returned.
func (ip *InstallationProcess) beforeExecute() (string, error) {
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

// Executes the installation process inside a specially created docker container.
func (ip *InstallationProcess) Execute() error {
	installScriptPath, err := ip.beforeExecute()
	if err != nil {
		return errors.WithStack(err)
	}

	ctx := context.Background()

	zap.S().Debugw(
		"creating server installer container",
		zap.String("server", ip.Server.Uuid),
		zap.String("script_path", installScriptPath+"/install.sh"),
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
				Source:   installScriptPath,
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
				"max-size": "20m",
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
		return errors.WithStack(err)
	}

	zap.S().Infow("running installation process for server", zap.String("server", ip.Server.Uuid))
	if err := ip.client.ContainerStart(ctx, r.ID, types.ContainerStartOptions{}); err != nil {
		return err
	}

	stream, err := ip.client.ContainerAttach(ctx, r.ID, types.ContainerAttachOptions{
		Stdout: true,
		Stderr: true,
		Stream: true,
	})

	if err != nil {
		return errors.WithStack(err)
	}

	wg := sync.WaitGroup{}
	wg.Add(1)

	go func() {
		defer stream.Close()
		defer wg.Done()

		io.Copy(os.Stdout, stream.Reader)
	}()

	wg.Wait()

	zap.S().Infow("completed installation process", zap.String("server", ip.Server.Uuid))

	return nil
}
