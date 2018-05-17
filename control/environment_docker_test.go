package control

import (
	"context"
	"fmt"
	"testing"

	"github.com/pterodactyl/wings/api/websockets"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/client"
	"github.com/pterodactyl/wings/config"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
)

func testServer() *ServerStruct {
	viper.SetDefault(config.DataPath, "./test_data")
	return &ServerStruct{
		ID: "testuuid-something-something",
		Service: &Service{
			DockerImage: "alpine:latest",
		},
		StartupCommand: "/bin/ash echo hello && sleep 100",
		websockets:     websockets.NewCollection(),
	}
}

func TestNewDockerEnvironment(t *testing.T) {
	env, err := createTestDockerEnv(nil)

	assert.Nil(t, err)
	assert.NotNil(t, env)
	assert.NotNil(t, env.client)
}

func TestNewDockerEnvironmentExisting(t *testing.T) {
	eenv, _ := createTestDockerEnv(nil)
	eenv.Create()

	env, err := createTestDockerEnv(eenv.server)

	assert.Nil(t, err)
	assert.NotNil(t, env)
	assert.NotNil(t, env.server.DockerContainer)

	eenv.Destroy()
}

func TestCreateDockerEnvironment(t *testing.T) {
	env, _ := createTestDockerEnv(nil)

	err := env.Create()

	a := assert.New(t)
	a.Nil(err)
	a.NotNil(env.server.DockerContainer.ID)

	if err := env.client.ContainerRemove(context.TODO(), env.server.DockerContainer.ID, types.ContainerRemoveOptions{}); err != nil {
		fmt.Println(err)
	}
}

func TestDestroyDockerEnvironment(t *testing.T) {
	env, _ := createTestDockerEnv(nil)
	env.Create()

	err := env.Destroy()

	_, ierr := env.client.ContainerInspect(context.TODO(), env.server.DockerContainer.ID)

	assert.Nil(t, err)
	assert.True(t, client.IsErrNotFound(ierr))
}

func TestStartDockerEnvironment(t *testing.T) {
	env, _ := createTestDockerEnv(nil)
	env.Create()
	err := env.Start()

	i, ierr := env.client.ContainerInspect(context.TODO(), env.server.DockerContainer.ID)

	assert.Nil(t, err)
	assert.Nil(t, ierr)
	assert.True(t, i.State.Running)

	env.client.ContainerKill(context.TODO(), env.server.DockerContainer.ID, "KILL")
	env.Destroy()
}

func TestStopDockerEnvironment(t *testing.T) {
	env, _ := createTestDockerEnv(nil)
	env.Create()
	env.Start()
	err := env.Stop()

	i, ierr := env.client.ContainerInspect(context.TODO(), env.server.DockerContainer.ID)

	assert.Nil(t, err)
	assert.Nil(t, ierr)
	assert.False(t, i.State.Running)

	env.client.ContainerKill(context.TODO(), env.server.DockerContainer.ID, "KILL")
	env.Destroy()
}

func TestKillDockerEnvironment(t *testing.T) {
	env, _ := createTestDockerEnv(nil)
	env.Create()
	env.Start()
	err := env.Kill()

	i, ierr := env.client.ContainerInspect(context.TODO(), env.server.DockerContainer.ID)

	assert.Nil(t, err)
	assert.Nil(t, ierr)
	assert.False(t, i.State.Running)

	env.client.ContainerKill(context.TODO(), env.server.DockerContainer.ID, "KILL")
	env.Destroy()
}

func TestExecDockerEnvironment(t *testing.T) {

}

func createTestDockerEnv(s *ServerStruct) (*dockerEnvironment, error) {
	if s == nil {
		s = testServer()
	}
	env, err := NewDockerEnvironment(s)
	return env.(*dockerEnvironment), err
}
