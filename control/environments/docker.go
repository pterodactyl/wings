package environments

type DockerEnvironment struct {
	BaseEnvironment
}

// Ensure DockerEnvironment implements Environment
var _ Environment = &DockerEnvironment{}

func NewDockerEnvironment() *DockerEnvironment {
	return &DockerEnvironment{}
}

func (env *DockerEnvironment) Exec() error {
	return nil
}

func (env *DockerEnvironment) Create() error {

	return nil
}
