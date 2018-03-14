package control

// func testServer() *ServerStruct {
// 	return &ServerStruct{
// 		ID: "testuuid-something-something",
// 		service: &service{
// 			DockerImage: "alpine:latest",
// 		},
// 	}
// }

// func TestNewDockerEnvironment(t *testing.T) {
// 	env, err := createTestDockerEnv(nil)

// 	assert.Nil(t, err)
// 	assert.NotNil(t, env)
// 	assert.NotNil(t, env.client)
// }

// func TestNewDockerEnvironmentExisting(t *testing.T) {
// 	eenv, _ := createTestDockerEnv(nil)
// 	eenv.Create()

// 	env, err := createTestDockerEnv(eenv.server)

// 	assert.Nil(t, err)
// 	assert.NotNil(t, env)
// 	assert.NotNil(t, env.container)

// 	eenv.Destroy()
// }

// func TestCreateDockerEnvironment(t *testing.T) {
// 	env, _ := createTestDockerEnv(nil)

// 	err := env.Create()

// 	a := assert.New(t)
// 	a.Nil(err)
// 	a.NotNil(env.container)
// 	a.Equal(env.container.Name, "ptdl_testuuid")

// 	if err := env.client.RemoveContainer(docker.RemoveContainerOptions{
// 		ID: env.container.ID,
// 	}); err != nil {
// 		fmt.Println(err)
// 	}
// }

// func TestDestroyDockerEnvironment(t *testing.T) {
// 	env, _ := createTestDockerEnv(nil)
// 	env.Create()

// 	err := env.Destroy()

// 	_, ierr := env.client.InspectContainer(env.container.ID)

// 	assert.Nil(t, err)
// 	assert.IsType(t, ierr, &docker.NoSuchContainer{})
// }

// func TestStartDockerEnvironment(t *testing.T) {
// 	env, _ := createTestDockerEnv(nil)
// 	env.Create()
// 	err := env.Start()

// 	i, ierr := env.client.InspectContainer(env.container.ID)

// 	assert.Nil(t, err)
// 	assert.Nil(t, ierr)
// 	assert.True(t, i.State.Running)

// 	env.client.KillContainer(docker.KillContainerOptions{
// 		ID: env.container.ID,
// 	})
// 	env.Destroy()
// }

// func TestStopDockerEnvironment(t *testing.T) {
// 	env, _ := createTestDockerEnv(nil)
// 	env.Create()
// 	env.Start()
// 	err := env.Stop()

// 	i, ierr := env.client.InspectContainer(env.container.ID)

// 	assert.Nil(t, err)
// 	assert.Nil(t, ierr)
// 	assert.False(t, i.State.Running)

// 	env.client.KillContainer(docker.KillContainerOptions{
// 		ID: env.container.ID,
// 	})
// 	env.Destroy()
// }

// func TestKillDockerEnvironment(t *testing.T) {
// 	env, _ := createTestDockerEnv(nil)
// 	env.Create()
// 	env.Start()
// 	err := env.Kill()

// 	i, ierr := env.client.InspectContainer(env.container.ID)

// 	assert.Nil(t, err)
// 	assert.Nil(t, ierr)
// 	assert.False(t, i.State.Running)

// 	env.client.KillContainer(docker.KillContainerOptions{
// 		ID: env.container.ID,
// 	})
// 	env.Destroy()
// }

// func TestExecDockerEnvironment(t *testing.T) {

// }

// func createTestDockerEnv(s *ServerStruct) (*dockerEnvironment, error) {
// 	if s == nil {
// 		s = testServer()
// 	}
// 	env, err := NewDockerEnvironment(s)
// 	return env.(*dockerEnvironment), err
// }
