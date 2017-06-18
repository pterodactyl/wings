package environments

type DockerEnvironment struct {
	BaseEnvironment
}

func NewDockerEnvironment() *DockerEnvironment {
	return &DockerEnvironment{}
}

func (env *DockerEnvironment) Exec() error {

}

func (env *DockerEnvironment) Create() error {

	return nil
}
